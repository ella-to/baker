package baker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"ella.to/baker/internal/httpclient"
	"ella.to/baker/internal/trie"
)

type containerInfo struct {
	container *Container
	domain    string
	path      string
	pingCount int64
}

type Server struct {
	bufferSize    int
	pingDuration  time.Duration
	containersMap map[string]*containerInfo           // containerID -> containerInfo
	domainsMap    map[string]*trie.Node[[]*Container] // domain -> path -> containers
	runner        *ActionRunner
	close         chan struct{}
}

var _ http.Handler = (*Server)(nil)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	domain := r.Host
	path := r.URL.Path

	container := s.runner.Get(r.Context(), domain, path)
	if container == nil {
		http.NotFound(w, r)
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

	proxy.ServeHTTP(w, r)
}

func (s *Server) Close() {
	s.runner.Close()
	close(s.close)
}

func (s *Server) RegisterDriver(fn func(Driver)) {
	fn(s.runner)
}

func (s *Server) pingContainers() {
	// make a copy of the containers map
	containers := make([]*containerInfo, 0, len(s.containersMap))
	for _, cInfo := range s.containersMap {
		containers = append(containers, cInfo)
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

			config := Config{}
			if err := json.NewDecoder(rc).Decode(&config); err != nil {
				slog.Error("failed to decode container config", "container_id", c.Id, "url", url, "error", err)
				return
			}

			if statusCode >= 400 {
				slog.Error("container config endpoint returned an error", "container_id", c.Id, "url", url, "status_code", statusCode)
				return
			}

			for _, endpoint := range config.Endpoints {
				s.runner.Update(c, endpoint.Domain, endpoint.Path)
			}
		}(c, url, pingCount)
	}
}

func (s *Server) addContainer(container *Container) {
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
}

func (s *Server) updateContainer(container *Container, domain, path string) {
	cInfo, ok := s.containersMap[container.Id]
	if ok && cInfo.domain == domain && cInfo.path == path {
		// if the container is already in the correct domain and path, we don't need to do anything
		// we can just return to avoid unnecessary work
		return
	}

	paths, ok := s.domainsMap[domain]
	if !ok {
		paths = trie.New[[]*Container]()
		s.domainsMap[domain] = paths
	}

	containers := paths.Get([]rune(path))
	if containers == nil {
		containers = []*Container{container}
	} else {
		// we don't need to check if the container is already in the list, because we already checked that
		// in the beginning of this function
		containers = append(containers, container)
	}
	paths.Put([]rune(path), containers)

	// One thing to note that cInfo is not nil here
	// because we have intitalized it during the addContainer call
	// if it was nil, it should be a panic situation

	cInfo.domain = domain
	cInfo.path = path

	slog.Debug("container updated", "container_id", container.Id, "domain", domain, "path", path)

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

	containers := paths.Get([]rune(containerInfo.path))
	if containers == nil {
		return
	}

	for i, c := range containers {
		if c.Id != container.Id {
			continue
		}

		containers = append(containers[:i], containers[i+1:]...)
		if len(containers) == 0 {
			paths.Del([]rune(containerInfo.path))
		} else {
			paths.Put([]rune(containerInfo.path), containers)
		}
		break
	}
}

func (s *Server) getContainer(domain, path string) (container *Container) {
	defer func() {
		if container != nil {
			slog.Debug("found container", "container_id", container.Id, "domain", domain, "path", path)
		} else {
			slog.Debug("not found container", "domain", domain, "path", path)
		}
	}()

	paths, ok := s.domainsMap[domain]
	if !ok {
		return nil
	}

	containers := paths.Get([]rune(path))
	if len(containers) == 0 {
		return nil
	}

	// randomly select a container from the list
	// this is not the best way to do this, but it's good enough for now
	pos := rand.Int31n(int32(len(containers)))

	return containers[pos]
}

type serverOpt interface {
	configureServer(*Server)
}

type serverOptFunc func(*Server)

func (f serverOptFunc) configureServer(s *Server) {
	f(s)
}

func WithBufferSize(size int) serverOptFunc {
	return func(s *Server) {
		s.bufferSize = size
	}
}

func WithPingDuration(d time.Duration) serverOptFunc {
	return func(s *Server) {
		s.pingDuration = d
	}
}

func NewServer(opts ...serverOpt) *Server {
	s := &Server{
		bufferSize:    100,
		pingDuration:  10 * time.Second,
		containersMap: make(map[string]*containerInfo),
		domainsMap:    make(map[string]*trie.Node[[]*Container]),
		close:         make(chan struct{}),
	}

	for _, opt := range opts {
		opt.configureServer(s)
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
