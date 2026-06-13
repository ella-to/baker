package baker_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"ella.to/baker"
)

// staticContainer builds a container that registers immediately on Add (no ping
// required) by pointing a static domain/path at the given backend address.
func staticContainer(t testing.TB, id, domain, path, addr string) *baker.Container {
	t.Helper()
	c := &baker.Container{Id: id}
	if addr != "" {
		c.Addr = netip.MustParseAddrPort(addr)
	}
	c.Meta.Static.Domain = domain
	c.Meta.Static.Path = path
	return c
}

func backendAddr(t testing.TB, srv *httptest.Server) string {
	t.Helper()
	return strings.TrimPrefix(srv.URL, "http://")
}

// TestHealthCheckRemovesUnresponsiveContainer verifies that a dynamically
// registered container whose config endpoint starts failing is removed after
// maxPingFailures consecutive failures. This exercises the health-check path
// that was previously dead code (ping failure count never persisted).
func TestHealthCheckRemovesUnresponsiveContainer(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			if !healthy.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"endpoints":[{"domain":"health.example.com","path":"/*","rules":[]}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(backend.Close)

	handler := baker.NewServer(baker.WithPingDuration(50 * time.Millisecond))
	t.Cleanup(handler.Close)

	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) { driver = d })

	addr := netip.MustParseAddrPort(backendAddr(t, backend))
	driver.Add(&baker.Container{Id: "health-1", ConfigPath: "/config", Addr: addr})

	// Registered via a successful ping.
	waitForDomain(t, handler, "health.example.com")

	// Now make the backend unhealthy; failures must accumulate and remove it.
	healthy.Store(false)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		registered := handler.HasDomain(ctx, "health.example.com")
		cancel()
		if !registered {
			return // success: unresponsive container removed
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("unresponsive container was never removed by the health check")
}

// TestStreamingResponseFlushes verifies that streaming (SSE) responses are
// flushed through the proxy promptly. The backend blocks after the first chunk
// until the test has received it; if flushing did not propagate through baker's
// response-writer wrapper, the first read would block and the test would fail on
// the watchdog timeout.
func TestStreamingResponseFlushes(t *testing.T) {
	released := make(chan struct{})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("backend response writer is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: first\n\n")
		fl.Flush()
		<-released // hold the handler open until the test reads the first chunk
		_, _ = io.WriteString(w, "data: second\n\n")
		fl.Flush()
	}))
	t.Cleanup(backend.Close)

	handler, url := createBakerServer(t)
	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) { driver = d })
	driver.Add(staticContainer(t, "stream-1", "stream.example.com", "/*", backendAddr(t, backend)))
	waitForDomain(t, handler, "stream.example.com")

	req, err := http.NewRequest(http.MethodGet, url+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "stream.example.com"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	first := make([]byte, len("data: first\n\n"))
	readErr := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, first)
		readErr <- err
	}()

	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("reading first chunk: %v", err)
		}
		if !strings.Contains(string(first), "first") {
			t.Fatalf("unexpected first chunk: %q", string(first))
		}
	case <-time.After(3 * time.Second):
		close(released)
		t.Fatal("did not receive the flushed first chunk in time (flush not propagating through proxy)")
	}

	close(released)

	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading rest: %v", err)
	}
	if !strings.Contains(string(rest), "second") {
		t.Fatalf("did not receive second chunk, got: %q", string(rest))
	}
}

// TestWebSocketProxy verifies an end-to-end WebSocket round trip through baker.
func TestWebSocketProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(backend.Close)

	handler, url := createBakerServer(t)
	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) { driver = d })
	driver.Add(staticContainer(t, "ws-1", "ws.example.com", "/*", backendAddr(t, backend)))
	waitForDomain(t, handler, "ws.example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws://" + strings.TrimPrefix(url, "http://") + "/echo"
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{Host: "ws.example.com"})
	if err != nil {
		t.Fatalf("dial through baker: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	want := []byte("hello baker")
	if err := c.Write(ctx, websocket.MessageText, want); err != nil {
		t.Fatalf("write: %v", err)
	}

	typ, got, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText || string(got) != string(want) {
		t.Fatalf("echo mismatch: type=%v got=%q want=%q", typ, string(got), string(want))
	}
}

// TestRouteInfoReportsMatchedPattern verifies that baker surfaces the matched
// route pattern (used for bounded metric cardinality) rather than the raw URL.
func TestRouteInfoReportsMatchedPattern(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(backend.Close)

	handler := baker.NewServer(baker.WithPingDuration(time.Hour))
	t.Cleanup(handler.Close)
	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) { driver = d })
	driver.Add(staticContainer(t, "ri-1", "route.example.com", "/*", backendAddr(t, backend)))
	waitForDomain(t, handler, "route.example.com")

	// Matched route -> pattern is the registered "domain+path", not "/users/42".
	ctx, ri := baker.WithRouteInfo(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil).WithContext(ctx)
	req.Host = "route.example.com"
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if ri.Pattern != "route.example.com/*" {
		t.Fatalf("matched route pattern = %q, want %q", ri.Pattern, "route.example.com/*")
	}

	// Unmatched route -> pattern stays empty so the caller can bucket as "unmatched".
	ctx2, ri2 := baker.WithRouteInfo(context.Background())
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx2)
	req2.Host = "unknown.example.com"
	handler.ServeHTTP(httptest.NewRecorder(), req2)
	if ri2.Pattern != "" {
		t.Fatalf("unmatched route pattern = %q, want empty", ri2.Pattern)
	}
}

// TestReliableWritesRegisterAllContainers confirms that a burst of Add calls all
// take effect (no events are silently dropped, which the old buffered-channel
// driver did when the queue filled).
func TestReliableWritesRegisterAllContainers(t *testing.T) {
	handler := baker.NewServer(baker.WithPingDuration(time.Hour))
	t.Cleanup(handler.Close)

	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) { driver = d })

	const n = 500
	for i := range n {
		domain := fmt.Sprintf("burst%d.example.com", i)
		driver.Add(staticContainer(t, fmt.Sprintf("burst-%d", i), domain, "/*", ""))
	}

	ctx := context.Background()
	for i := range n {
		domain := fmt.Sprintf("burst%d.example.com", i)
		if !handler.HasDomain(ctx, domain) {
			t.Fatalf("domain %q was not registered (event dropped?)", domain)
		}
	}
}

// TestConcurrentAddRemoveServe hammers the routing table with concurrent reads
// (ServeHTTP / HasDomain) and writes (Add / Remove) to surface data races under
// -race and confirm the RWMutex-protected hot path is safe.
func TestConcurrentAddRemoveServe(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(backend.Close)
	addr := backendAddr(t, backend)

	handler := baker.NewServer(baker.WithPingDuration(time.Hour))
	t.Cleanup(handler.Close)
	var driver baker.Driver
	handler.RegisterDriver(func(d baker.Driver) { driver = d })

	const workers = 8
	const iterations = 300

	var wg sync.WaitGroup

	// Writers: continuously add and remove their own domain.
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			domain := fmt.Sprintf("conc%d.example.com", w)
			for i := range iterations {
				c := staticContainer(t, fmt.Sprintf("conc-%d-%d", w, i), domain, "/*", addr)
				driver.Add(c)
				driver.Remove(c)
			}
		}(w)
	}

	// Readers: continuously resolve routes via the hot path.
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			domain := fmt.Sprintf("conc%d.example.com", w)
			ctx := context.Background()
			for range iterations {
				_ = handler.HasDomain(ctx, domain)
				req := httptest.NewRequest(http.MethodGet, "/path", nil)
				req.Host = domain
				handler.ServeHTTP(httptest.NewRecorder(), req)
			}
		}(w)
	}

	wg.Wait()
}
