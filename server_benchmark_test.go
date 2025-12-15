package baker_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"ella.to/baker"
	"ella.to/baker/rule"
)

// BenchmarkServerServeHTTP benchmarks the main ServeHTTP handler
func BenchmarkServerServeHTTP(b *testing.B) {
	container := createBenchContainer(b, "bench.example.com", "/*")
	handler := baker.NewServer(
		baker.WithPingDuration(1*time.Hour), // Disable auto-ping
		baker.WithRules(
			rule.RegisterAppendPath(),
			rule.RegisterReplacePath(),
			rule.RegisterRateLimiter(),
		),
	)

	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) {
		driver = d
	})
	driver.Add(container)

	// Wait for container to be registered
	time.Sleep(3 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	req.Host = "bench.example.com"

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rr := httptest.NewRecorder()
		for pb.Next() {
			handler.ServeHTTP(rr, req)
		}
	})

	b.Cleanup(func() {
		handler.Close()
	})
}

// BenchmarkServerWithRateLimiter benchmarks server with rate limiter middleware
func BenchmarkServerWithRateLimiter(b *testing.B) {
	container := createBenchContainerWithRateLimiter(b)
	handler := baker.NewServer(
		baker.WithPingDuration(1*time.Hour),
		baker.WithRules(
			rule.RegisterRateLimiter(),
		),
	)

	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) {
		driver = d
	})
	driver.Add(container)

	time.Sleep(3 * time.Second)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/rate-limited", nil)
		req.Host = "ratelimit.example.com"
		rr := httptest.NewRecorder()
		for pb.Next() {
			handler.ServeHTTP(rr, req)
		}
	})

	b.Cleanup(func() {
		handler.Close()
	})
}

// BenchmarkServerMultipleContainers benchmarks with multiple containers registered
func BenchmarkServerMultipleContainers(b *testing.B) {
	handler := baker.NewServer(
		baker.WithPingDuration(1*time.Hour),
		baker.WithRules(
			rule.RegisterAppendPath(),
		),
	)

	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) {
		driver = d
	})

	// Add multiple containers
	for i := 0; i < 10; i++ {
		domain := fmt.Sprintf("multi%d.example.com", i)
		container := createBenchContainer(b, domain, "/*")
		driver.Add(container)
	}

	time.Sleep(3 * time.Second)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/path", nil)
		req.Host = "multi5.example.com" // Hit container 5
		rr := httptest.NewRecorder()
		for pb.Next() {
			handler.ServeHTTP(rr, req)
		}
	})

	b.Cleanup(func() {
		handler.Close()
	})
}

// BenchmarkServerNotFound benchmarks 404 response path
func BenchmarkServerNotFound(b *testing.B) {
	handler := baker.NewServer(
		baker.WithPingDuration(1 * time.Hour),
	)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
		req.Host = "unknown.example.com"
		rr := httptest.NewRecorder()
		for pb.Next() {
			handler.ServeHTTP(rr, req)
		}
	})

	b.Cleanup(func() {
		handler.Close()
	})
}

// Helper functions for creating benchmark containers
var (
	benchContainerCount int
	benchMu             sync.Mutex
)

func createBenchContainer(tb testing.TB, domain, path string) *baker.Container {
	config := fmt.Sprintf(`{"endpoints":[{"domain":"%s","path":"%s","rules":[]}]}`, domain, path)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(config))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))

	tb.Cleanup(server.Close)

	benchMu.Lock()
	benchContainerCount++
	id := benchContainerCount
	benchMu.Unlock()

	addr, _ := netip.ParseAddrPort(strings.TrimPrefix(server.URL, "http://"))
	return &baker.Container{
		Id:         fmt.Sprintf("bench-container-%d", id),
		ConfigPath: "/config",
		Addr:       addr,
	}
}

func createBenchContainerWithRateLimiter(tb testing.TB) *baker.Container {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"endpoints":[{
					"domain":"ratelimit.example.com",
					"path":"/*",
					"rules":[{"type":"RateLimiter","args":{"request_limit":1000000,"window_duration":"1m"}}]
				}]
			}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))

	tb.Cleanup(server.Close)

	benchMu.Lock()
	benchContainerCount++
	id := benchContainerCount
	benchMu.Unlock()

	addr, _ := netip.ParseAddrPort(strings.TrimPrefix(server.URL, "http://"))
	return &baker.Container{
		Id:         fmt.Sprintf("rate-container-%d", id),
		ConfigPath: "/config",
		Addr:       addr,
	}
}
