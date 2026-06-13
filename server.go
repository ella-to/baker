package baker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"ella.to/baker/internal/collection"
	"ella.to/baker/internal/httpclient"
	"ella.to/baker/internal/trie"
	"ella.to/baker/rule"
)

// maxPingFailures is the number of consecutive failed pings after which an
// unresponsive container is removed from the routing table.
const maxPingFailures = 3

// pingTimeout bounds each per-container config fetch during a ping cycle.
const pingTimeout = 2 * time.Second

type containerInfo struct {
	container *Container
	domain    string
	path      string
	pingCount int64
}

type Server struct {
	bufferSize   int
	pingDuration time.Duration

	// mu guards containersMap and domainsMap. The request hot path takes the
	// read lock so lookups run concurrently across cores; the infrequent
	// mutations (driver add/remove, ping-driven updates) take the write lock.
	mu            sync.RWMutex
	containersMap map[string]*containerInfo       // containerID -> containerInfo
	domainsMap    map[string]*trie.Node[*Service] // domain -> path -> containers

	rules              map[string]rule.BuilderFunc
	middlewareCacheMap *collection.Map[rule.Middleware]
	proxy              *httputil.ReverseProxy
	pingClient         httpclient.Getter
	runner             *ActionRunner
	close              chan struct{}
	isDebug            bool
}

var _ http.Handler = (*Server)(nil)

type trackResponseWriter struct {
	statusCode int
	w          http.ResponseWriter
}

var _ http.Hijacker = (*trackResponseWriter)(nil)

func (t *trackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := t.w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}

	return h.Hijack()
}

var _ http.ResponseWriter = (*trackResponseWriter)(nil)

func (t *trackResponseWriter) Header() http.Header {
	return t.w.Header()
}

func (t *trackResponseWriter) Write(p []byte) (int, error) {
	return t.w.Write(p)
}

func (t *trackResponseWriter) WriteHeader(code int) {
	t.statusCode = code
	t.w.WriteHeader(code)
}

// Flush forwards to the underlying writer when it supports flushing so that
// streaming responses (SSE, chunked transfer) reach the client promptly.
// httputil.ReverseProxy type-asserts the response writer for http.Flusher; if
// this wrapper did not implement it, streaming responses would buffer.
func (t *trackResponseWriter) Flush() {
	if f, ok := t.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer so http.ResponseController can reach
// capabilities not promoted through this wrapper.
func (t *trackResponseWriter) Unwrap() http.ResponseWriter {
	return t.w
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Connection")) == "upgrade" && strings.ToLower(r.Header.Get("Upgrade")) == "websocket"
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request, container *Container, endpoint *Endpoint) {
	middlewares, err := s.getMiddlewares(endpoint)
	if err != nil {
		slog.Error("failed to get middlewares", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	target := &proxyTarget{
		host:    container.Addr.String(),
		headers: container.Meta.Static.Headers,
	}
	r = r.WithContext(withProxyTarget(r.Context(), target))

	rule.Chain(s.proxy, middlewares...).ServeHTTP(w, r)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request, container *Container) {
	targetURL := &url.URL{
		Scheme: "ws",
		Host:   container.Addr.String(),
		Path:   r.URL.Path,
	}

	host := r.Host

	for k, v := range container.Meta.Static.Headers {
		key := strings.ToUpper(k)
		if key == "HOST" {
			host = v
			break
		}
	}

	clientConn, _, err := websocket.Dial(r.Context(), targetURL.String(), &websocket.DialOptions{
		HTTPHeader: r.Header,
		Host:       host,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error connecting to backend server: %s", err), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	serverConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error connecting to backend server: %s", err), http.StatusInternalServerError)
		return
	}
	defer serverConn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Proxy data in both directions. Each direction uses its own error variable
	// to avoid a data race on a shared one, and cancels the shared context when
	// it finishes so the other direction unblocks and the connections close.
	go func() {
		defer cancel()
		if err := copyWebsocketStream(ctx, clientConn, serverConn); err != nil {
			slog.Error("failed to copy data between server and client", "error", err)
		}
	}()

	if err := copyWebsocketStream(ctx, serverConn, clientConn); err != nil {
		slog.Error("failed to copy data between client and server", "error", err)
	}
}

func copyWebsocketStream(ctx context.Context, dst, src *websocket.Conn) error {
	var msgType websocket.MessageType
	var r io.Reader
	var w io.WriteCloser
	var err error

	for {
		msgType, r, err = src.Reader(ctx)
		if err != nil {
			break
		}

		w, err = dst.Writer(ctx, msgType)
		if err != nil {
			break
		}

		_, err = io.Copy(w, r)
		if err != nil {
			_ = w.Close()
			break
		}

		// The writer must be closed to flush the WebSocket frame (set the FIN
		// bit) to the other peer. Without this, messages are buffered and never
		// delivered.
		err = w.Close()
		if err != nil {
			break
		}
	}

	if errors.Is(err, context.Canceled) {
		return nil
	} else if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	domain := r.Host
	path := r.URL.Path

	tw := &trackResponseWriter{w: w}

	// Resolve the route directly under the read lock. This is the hot path: it
	// runs concurrently across cores instead of serializing through the action
	// runner goroutine.
	container, endpoint := s.getContainer(domain, path)
	if container == nil {
		tw.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(tw, "not found, domain: %s, path: %s", domain, path)
		return
	}

	// Surface the matched route pattern for any outer observability middleware
	// so metric cardinality stays bounded by registered routes, not raw URLs.
	if ri := routeInfoFromContext(r.Context()); ri != nil {
		ri.Pattern = endpoint.Domain + endpoint.Path
	}

	if isWebSocketRequest(r) {
		s.handleWebSocket(tw, r, container)
	} else {
		s.handleHTTP(tw, r, container, endpoint)
	}
}

func (s *Server) Close() {
	s.runner.Close()
	close(s.close)
}

func (s *Server) RegisterDriver(fn func(Driver)) {
	fn(s.runner)
}

func (s *Server) getMiddlewares(endpoint *Endpoint) ([]rule.Middleware, error) {
	if len(endpoint.Rules) == 0 {
		return rule.Empty, nil
	}

	middlewares := make([]rule.Middleware, 0)

	for _, r := range endpoint.Rules {
		builder, ok := s.rules[r.Type]
		if !ok {
			return nil, fmt.Errorf("failed to find rule builder for %s", r.Type)
		}

		middleware, err := builder(r.Args)
		if err != nil {
			return nil, fmt.Errorf("failed to parse args for rule %s: %w", r.Type, err)
		}

		if middleware.IsCachable() {
			middleware = s.middlewareCacheMap.GetAndUpdate(endpoint.getHashKey(), func(old rule.Middleware, found bool) rule.Middleware {
				if found {
					return old.UpdateMiddleware(middleware)
				}

				return middleware.UpdateMiddleware(nil)
			})
		}

		middlewares = append(middlewares, middleware)
	}

	return middlewares, nil
}

func (s *Server) pingContainers() {
	// Snapshot the containers under the read lock, then release it before doing
	// any network I/O or taking the write lock (which the static re-register and
	// per-container updates below do).
	s.mu.RLock()
	dynamic := make([]*Container, 0, len(s.containersMap))
	static := make([]*Container, 0)
	for _, cInfo := range s.containersMap {
		// if a container has a static domain configuration, we don't need to ping it
		if cInfo.container.Meta.Static.Domain == "" {
			dynamic = append(dynamic, cInfo.container)
		} else {
			static = append(static, cInfo.container)
		}
	}
	s.mu.RUnlock()

	// Re-assert static routes (idempotent; updateContainerLocked no-ops when the
	// domain/path are unchanged).
	for _, c := range static {
		s.registerStaticContainer(c)
	}

	// ping all the dynamic containers concurrently
	for _, c := range dynamic {
		url := fmt.Sprintf("http://%s%s", c.Addr, c.ConfigPath)

		go func(c *Container, url string) {
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			defer cancel()

			rc, statusCode, err := s.pingClient.Get(ctx, url)
			if err != nil {
				slog.Error("failed to call container config endpoint", "container_id", c.Id, "url", url, "error", err)
				s.recordPingFailure(c)
				return
			}
			defer rc.Close()

			if statusCode >= 400 {
				slog.Error("container config endpoint returned an error", "container_id", c.Id, "url", url, "status_code", statusCode)
				s.recordPingFailure(c)
				return
			}

			config, err := s.parseConfig(rc)
			if err != nil {
				slog.Error("failed to read container config", "container_id", c.Id, "url", url, "error", err)
				s.recordPingFailure(c)
				return
			}

			s.recordPingSuccess(c.Id)

			for i := range config.Endpoints {
				s.updateContainer(c, &config.Endpoints[i])
			}
		}(c, url)
	}
}

func (s *Server) parseConfig(rc io.ReadCloser) (*Config, error) {
	config := &Config{}

	if s.isDebug {
		payload, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}

		slog.Debug("parsing config payload", "payload", string(payload))

		if err := json.Unmarshal(payload, config); err != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
	} else {
		if err := json.NewDecoder(rc).Decode(config); err != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
	}

	return config, nil
}

func (s *Server) addContainer(container *Container) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addContainerLocked(container)
}

func (s *Server) addContainerLocked(container *Container) {
	_, ok := s.containersMap[container.Id]
	if ok {
		// usually this should not happen, but if it does, we can just
		// return to avoid unnecessary work
		slog.Warn("container already exists", "container_id", container.Id)
		return
	}

	s.containersMap[container.Id] = &containerInfo{
		container: container,
		domain:    "",
		path:      "",
	}

	s.registerStaticContainerLocked(container)
}

func (s *Server) registerStaticContainer(container *Container) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerStaticContainerLocked(container)
}

func (s *Server) registerStaticContainerLocked(container *Container) {
	if container.Meta.Static.Domain == "" {
		return
	}

	s.updateContainerLocked(container, &Endpoint{
		Domain: container.Meta.Static.Domain,
		Path:   container.Meta.Static.Path,
		Rules:  []Rule{},
	})
}

func (s *Server) updateContainer(container *Container, endpoint *Endpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateContainerLocked(container, endpoint)
}

func (s *Server) updateContainerLocked(container *Container, endpoint *Endpoint) {
	cInfo, ok := s.containersMap[container.Id]
	if ok && cInfo.domain == endpoint.Domain && cInfo.path == endpoint.Path {
		// if the container is already in the correct domain and path, we don't need to do anything
		// we can just return to avoid unnecessary work
		return
	}

	paths, ok := s.domainsMap[endpoint.Domain]
	if !ok {
		paths = trie.New[*Service]()
		s.domainsMap[endpoint.Domain] = paths
	}

	service := paths.Get([]rune(endpoint.Path))
	if service == nil {
		service = &Service{
			Containers: []*Container{container},
			Endpoint:   endpoint,
		}
	} else {
		// we don't need to check if the container is already in the list, because we already checked that
		// in the beginning of this function
		service.Containers = append(service.Containers, container)
	}
	paths.Put([]rune(endpoint.Path), service)

	// One thing to note that cInfo is not nil here
	// because we have intitalized it during the addContainer call
	// if it was nil, it should be a panic situation

	cInfo.domain = endpoint.Domain
	cInfo.path = endpoint.Path

	slog.Debug("container updated", "container_id", container.Id, "domain", endpoint.Domain, "path", endpoint.Path)

	s.containersMap[container.Id] = cInfo
}

func (s *Server) removeContainer(container *Container) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeContainerLocked(container)
}

// recordPingFailure increments the consecutive failure count for a container
// and removes it once it crosses maxPingFailures, so dead backends stop
// receiving traffic even when no driver "remove" event arrives.
func (s *Server) recordPingFailure(container *Container) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cInfo, ok := s.containersMap[container.Id]
	if !ok {
		return
	}

	cInfo.pingCount++
	if cInfo.pingCount > maxPingFailures {
		slog.Error("container is not responding, removing", "container_id", container.Id, "failures", cInfo.pingCount)
		s.removeContainerLocked(container)
	}
}

// recordPingSuccess resets the failure count after a healthy ping.
func (s *Server) recordPingSuccess(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cInfo, ok := s.containersMap[id]; ok {
		cInfo.pingCount = 0
	}
}

func (s *Server) removeContainerLocked(container *Container) {
	containerInfo, ok := s.containersMap[container.Id]
	if !ok {
		return
	}

	delete(s.containersMap, container.Id)

	slog.Debug("container removed", "container_id", container.Id)

	paths, ok := s.domainsMap[containerInfo.domain]
	if !ok {
		return
	}

	service := paths.Get([]rune(containerInfo.path))
	if service == nil {
		return
	}

	for i, c := range service.Containers {
		if c.Id != container.Id {
			continue
		}

		service.Containers = append(service.Containers[:i], service.Containers[i+1:]...)
		if len(service.Containers) == 0 {
			paths.Del([]rune(containerInfo.path))
			s.middlewareCacheMap.Delete(service.Endpoint.getHashKey())
			// Drop the domain entirely once it has no remaining routes, so
			// HasDomain (used by ACME) does not report stale domains and the
			// map does not retain empty tries.
			if paths.Size() == 0 {
				delete(s.domainsMap, containerInfo.domain)
			}
		} else {
			paths.Put([]rune(containerInfo.path), service)
		}
		break
	}
}

func (s *Server) getContainer(domain, path string) (container *Container, endpoint *Endpoint) {
	defer func() {
		if container != nil {
			slog.Debug("found container", "container_id", container.Id, "domain", domain, "path", path)
		} else {
			slog.Debug("not found container", "domain", domain, "path", path)
		}
	}()

	s.mu.RLock()
	defer s.mu.RUnlock()

	paths, ok := s.domainsMap[domain]
	if !ok {
		return nil, nil
	}

	service := paths.GetString(path)
	if service == nil || len(service.Containers) == 0 {
		return nil, nil
	}

	// randomly select a container from the list
	// this is not the best way to do this, but it's good enough for now
	pos := rand.IntN(len(service.Containers))

	return service.Containers[pos], service.Endpoint
}

// HasDomain checks if a domain is registered with the server.
// This can be used by ACME to validate domains before requesting certificates.
func (s *Server) HasDomain(ctx context.Context, domain string) bool {
	return s.hasDomain(domain)
}

func (s *Server) hasDomain(domain string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.domainsMap[domain]
	return ok
}

type serverOpt interface {
	configureServer(*Server) error
}

type serverOptFunc func(*Server) error

func (f serverOptFunc) configureServer(s *Server) error {
	return f(s)
}

func WithBufferSize(size int) serverOptFunc {
	return func(s *Server) error {
		s.bufferSize = size
		return nil
	}
}

func WithPingDuration(d time.Duration) serverOptFunc {
	return func(s *Server) error {
		s.pingDuration = d
		return nil
	}
}

func WithRules(rules ...rule.RegisterFunc) serverOptFunc {
	return func(s *Server) error {
		s.rules = make(map[string]rule.BuilderFunc)

		for _, r := range rules {
			if err := r(s.rules); err != nil {
				return err
			}
		}

		return nil
	}
}

func NewServer(opts ...serverOpt) *Server {
	logLevel := strings.ToLower(os.Getenv("BAKER_LOG_LEVEL"))

	s := &Server{
		bufferSize:         100,
		pingDuration:       10 * time.Second,
		containersMap:      make(map[string]*containerInfo),
		domainsMap:         make(map[string]*trie.Node[*Service]),
		middlewareCacheMap: collection.NewMap[rule.Middleware](),
		proxy:              newReverseProxy(),
		close:              make(chan struct{}),
		isDebug:            logLevel == "debug",
	}

	// A single reused client for health pings; its idle connections are pooled
	// across ping cycles instead of being rebuilt each time.
	pingClient, err := httpclient.NewClient(httpclient.WithHttpClientTimeout(pingTimeout, ""))
	if err != nil {
		slog.Error("failed to create ping http client", "error", err)
		return nil
	}
	s.pingClient = pingClient

	for _, opt := range opts {
		if err := opt.configureServer(s); err != nil {
			slog.Error("failed to configure server", "error", err)
			return nil
		}
	}

	s.runner = NewActionRunner(
		s.bufferSize,
		WithPingerCallback(s.pingContainers),
		WithAddCallback(s.addContainer),
		WithUpdateCallback(s.updateContainer),
		WithRemoveCallback(s.removeContainer),
		WithGetCallback(s.getContainer),
		WithHasDomainCallback(s.hasDomain),
	)

	go func() {
		defer slog.Debug("Server: stopped")

		for {
			select {
			case <-s.close:
				return
			case <-time.After(s.pingDuration):
				s.runner.Pinger()
			}
		}
	}()

	return s
}
