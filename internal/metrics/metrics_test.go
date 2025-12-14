package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ella.to/baker/internal/metrics"
)

func TestSetupHandler(t *testing.T) {
	t.Run("returns valid handler", func(t *testing.T) {
		handler := metrics.SetupHandler()
		assert.NotNil(t, handler)
	})

	t.Run("metrics endpoint returns 200", func(t *testing.T) {
		handler := metrics.SetupHandler()

		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("metrics endpoint returns prometheus format", func(t *testing.T) {
		handler := metrics.SetupHandler()

		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		body, err := io.ReadAll(rr.Body)
		require.NoError(t, err)

		assert.Contains(t, string(body), "go_")
	})
}

func TestSetInfo(t *testing.T) {
	t.Run("sets version and commit info", func(t *testing.T) {
		metrics.SetInfo("1.0.0", "abc123")

		handler := metrics.SetupHandler()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		body, err := io.ReadAll(rr.Body)
		require.NoError(t, err)

		bodyStr := string(body)
		assert.Contains(t, bodyStr, "baker_info")
	})
}

func TestHttpRequestCount(t *testing.T) {
	t.Run("increments counter", func(t *testing.T) {
		metrics.HttpRequestCount("example.com", "/api/users", "GET", 200)

		handler := metrics.SetupHandler()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		body, err := io.ReadAll(rr.Body)
		require.NoError(t, err)

		bodyStr := string(body)
		assert.Contains(t, bodyStr, "baker_http_request_count")
	})
}

func TestHttpRequestDuration(t *testing.T) {
	t.Run("records duration histogram", func(t *testing.T) {
		metrics.HttpRequestDuration("example.com", "/api/data", "GET", 200, 0.5)

		handler := metrics.SetupHandler()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		body, err := io.ReadAll(rr.Body)
		require.NoError(t, err)

		bodyStr := string(body)
		assert.Contains(t, bodyStr, "baker_http_request_duration_seconds")
	})
}

func TestWebsocketRequest(t *testing.T) {
	t.Run("increments websocket counter", func(t *testing.T) {
		metrics.WebsocketRequest("ws.example.com", "/socket", "GET", 101)

		handler := metrics.SetupHandler()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		body, err := io.ReadAll(rr.Body)
		require.NoError(t, err)

		bodyStr := string(body)
		assert.Contains(t, bodyStr, "baker_websocket_request_count")
	})
}

func BenchmarkHttpRequestCount(b *testing.B) {
	for i := 0; i < b.N; i++ {
		metrics.HttpRequestCount("example.com", "/api/users", "GET", 200)
	}
}

func BenchmarkHttpRequestDuration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		metrics.HttpRequestDuration("example.com", "/api/users", "GET", 200, 0.1)
	}
}

func BenchmarkMetricsHandler(b *testing.B) {
	for i := 0; i < 100; i++ {
		metrics.HttpRequestCount("example.com", "/api", "GET", 200)
	}

	handler := metrics.SetupHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}
