package baker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"ella.to/baker/internal/collection"
	"ella.to/baker/internal/httpclient"
	"ella.to/baker/internal/metrics"
	"ella.to/baker/internal/trie"
	"ella.to/baker/rule"
)

type containerInfo struct {
	container *Container
	meta      *MetaData
	domain    string
	path      string
	pingCount int64
}

type Server struct {
	bufferSize         int
	pingDuration       time.Duration
	containersMap      map[string]*containerInfo       // containerID -> containerInfo
	domainsMap         map[string]*trie.Node[*Service] // domain -> path -> containers
	rules              map[string]rule.BuilderFunc
	middlewareCacheMap *collection.Map[rule.Middleware]
	runner             *ActionRunner
	close              chan struct{}
	isDebug            bool
}

var _ http.Handler = (*Server)(nil)

type trackResponseWriter struct {
	statusCode int
	w          http.ResponseWriter
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

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	domain := r.Host
	path := r.URL.Path
	method := r.Method

	tw := &trackResponseWriter{w: w}

	start := time.Now()
	defer func() {
		metrics.HttpProcessedRequest(domain, path, method, tw.statusCode)
		metrics.HttpRequestDuration(domain, path, method, tw.statusCode, float64(time.Since(start)))
	}()

	var container *Container
	endpoint := &Endpoint{
		Domain: domain,
		Path:   path,
	}

	container, endpoint = s.runner.Get(r.Context(), endpoint)
	if container == nil {
		tw.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(tw, "not found, domain: %s, path: %s", domain, path)
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			url := &url.URL{
				Scheme: "http",
				Host:   container.Addr.String(),
			}

			slog.Debug("rewriting url", "from", r.In.URL.String(), "to", url.String())

			r.SetURL(url)     // Forward request to outboundURL.
			r.SetXForwarded() // Set X-Forwarded-* headers.
		},
	}

	middlewares, err := s.getMiddlewares(endpoint)
	if err != nil {
		slog.Error("failed to get middlewares", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rule.Chain(proxy, middlewares...).ServeHTTP(tw, r)
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
					return old.UpdateMiddelware(middleware)
				}

				return middleware.UpdateMiddelware(nil)
			})
		}

		middlewares = append(middlewares, middleware)
	}

	return middlewares, nil
}

func (s *Server) pingContainers() {
	// make a copy of the containers map
	containers := make([]*containerInfo, 0, len(s.containersMap))
	for _, cInfo := range s.containersMap {
		// if container has a static domain configuration, we dont need to ping it
		if cInfo.meta == nil || cInfo.meta.StaticDomain == "" {
			containers = append(containers, cInfo)
		} else {
			s.runner.Update(cInfo.container, &Endpoint{
				Domain: cInfo.meta.StaticDomain,
				Path:   cInfo.meta.StaticPath,
				Rules:  []Rule{},
			})
		}
	}

	getter, err := httpclient.NewClient(httpclient.WithHttpClientTimeout(2*time.Second, ""))
	if err != nil {
		slog.Error("failed to create http client", "error", err)
		return
	}

	// ping all the containers
	for _, ci := range containers {
		// Copy the base info to prevent data race
		pingCount := ci.pingCount + 1
		url := fmt.Sprintf("http://%s%s", ci.container.Addr, ci.container.ConfigPath)
		c := &Container{
			Id:         ci.container.Id,
			ConfigPath: ci.container.ConfigPath,
			Addr:       ci.container.Addr,
		}

		go func(c *Container, url string, pingCount int64) {
			if pingCount > 3 {
				slog.Error("container is not responding", "container_id", c.Id, "ping_count", pingCount)
				s.runner.Remove(c)
				return
			}

			ctx := context.Background()

			rc, statusCode, err := getter.Get(ctx, url)
			if err != nil {
				slog.Error("failed to call container config endpoint", "container_id", c.Id, "url", url, "error", err)
				return
			}
			defer rc.Close()

			config, err := s.parseConfig(rc)
			if err != nil {
				slog.Error("failed to read container config", "container_id", c.Id, "url", url, "error", err)
				return
			}

			if statusCode >= 400 {
				slog.Error("container config endpoint returned an error", "container_id", c.Id, "url", url, "status_code", statusCode)
				return
			}

			for _, endpoint := range config.Endpoints {
				s.runner.Update(c, &endpoint)
			}
		}(c, url, pingCount)
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

func (s *Server) addContainer(container *Container, meta *MetaData) {
	_, ok := s.containersMap[container.Id]
	if ok {
		// usually this should not happen, but if it does, we can just
		// return to avoid unnecessary work
		slog.Warn("container already exists", "container_id", container.Id)
		return
	}

	s.containersMap[container.Id] = &containerInfo{
		container: container,
		meta:      meta,
		domain:    "",
		path:      "",
	}
}

func (s *Server) updateContainer(container *Container, endpoint *Endpoint) {
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

	paths, ok := s.domainsMap[domain]
	if !ok {
		return nil, nil
	}

	service := paths.Get([]rune(path))
	if service == nil || len(service.Containers) == 0 {
		return nil, nil
	}

	// randomly select a container from the list
	// this is not the best way to do this, but it's good enough for now
	pos := rand.Int31n(int32(len(service.Containers)))

	return service.Containers[pos], service.Endpoint
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
		close:              make(chan struct{}),
		isDebug:            logLevel == "debug",
	}

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
