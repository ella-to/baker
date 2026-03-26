package rule_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ella.to/baker/rule"
)

func TestWindowDuration(t *testing.T) {
	t.Run("marshal JSON", func(t *testing.T) {
		wd := rule.WindowDuration{Duration: 5 * time.Second}
		data, err := json.Marshal(wd)
		require.NoError(t, err)
		assert.Equal(t, `"5s"`, string(data))
	})

	t.Run("unmarshal JSON", func(t *testing.T) {
		var wd rule.WindowDuration
		err := json.Unmarshal([]byte(`"10m30s"`), &wd)
		require.NoError(t, err)
		assert.Equal(t, 10*time.Minute+30*time.Second, wd.Duration)
	})

	t.Run("unmarshal invalid JSON", func(t *testing.T) {
		var wd rule.WindowDuration
		err := json.Unmarshal([]byte(`"invalid"`), &wd)
		assert.Error(t, err)
	})
}

func TestRateLimiter(t *testing.T) {
	t.Run("is cachable", func(t *testing.T) {
		rl := &rule.RateLimiter{}
		assert.True(t, rl.IsCachable())
	})

	t.Run("update middleware with nil initializes", func(t *testing.T) {
		rl := &rule.RateLimiter{
			RequestLimit:   10,
			WindowDuration: rule.WindowDuration{Duration: time.Minute},
		}

		result := rl.UpdateMiddleware(nil)
		assert.NotNil(t, result)
		assert.Equal(t, rl, result)
	})
}

func TestRateLimiterIntegration(t *testing.T) {
	t.Run("allows requests within limit", func(t *testing.T) {
		rl := &rule.RateLimiter{
			RequestLimit:   5,
			WindowDuration: rule.WindowDuration{Duration: time.Minute},
		}
		rl.UpdateMiddleware(nil)

		handler := rl.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for i := 0; i < 5; i++ {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "192.168.1.1:12345"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}
	})

	t.Run("blocks requests exceeding limit", func(t *testing.T) {
		rl := &rule.RateLimiter{
			RequestLimit:   2,
			WindowDuration: rule.WindowDuration{Duration: time.Minute},
		}
		rl.UpdateMiddleware(nil)

		handler := rl.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "192.168.1.2:12345"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.168.1.2:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	})

	t.Run("bucket algorithm allows requests within limit", func(t *testing.T) {
		rl := &rule.RateLimiter{
			Algo:           rule.RateLimiterAlgoBucket,
			RequestLimit:   5,
			WindowDuration: rule.WindowDuration{Duration: time.Minute},
		}
		rl.UpdateMiddleware(nil)

		handler := rl.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for i := 0; i < 5; i++ {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "192.168.1.3:12345"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}
	})

	t.Run("bucket algorithm blocks requests exceeding limit", func(t *testing.T) {
		rl := &rule.RateLimiter{
			Algo:           rule.RateLimiterAlgoBucket,
			RequestLimit:   2,
			WindowDuration: rule.WindowDuration{Duration: time.Minute},
		}
		rl.UpdateMiddleware(nil)

		handler := rl.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = "192.168.1.4:12345"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.168.1.4:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	})
}

func TestRegisterRateLimiter(t *testing.T) {
	m := make(map[string]rule.BuilderFunc)
	err := rule.RegisterRateLimiter()(m)
	require.NoError(t, err)

	builder, ok := m["RateLimiter"]
	require.True(t, ok)

	t.Run("window algorithm (default)", func(t *testing.T) {
		middleware, err := builder(json.RawMessage(`{"request_limit":100,"window_duration":"1m"}`))
		require.NoError(t, err)

		rl, ok := middleware.(*rule.RateLimiter)
		require.True(t, ok)
		assert.Equal(t, 100, rl.RequestLimit)
		assert.Equal(t, time.Minute, rl.WindowDuration.Duration)
		assert.Equal(t, rule.RateLimiterAlgo(""), rl.Algo) // defaults to window when empty
	})

	t.Run("window algorithm explicit", func(t *testing.T) {
		middleware, err := builder(json.RawMessage(`{"algo":"window","request_limit":100,"window_duration":"1m"}`))
		require.NoError(t, err)

		rl, ok := middleware.(*rule.RateLimiter)
		require.True(t, ok)
		assert.Equal(t, rule.RateLimiterAlgoWindow, rl.Algo)
	})

	t.Run("bucket algorithm", func(t *testing.T) {
		middleware, err := builder(json.RawMessage(`{"algo":"bucket","request_limit":50,"window_duration":"30s"}`))
		require.NoError(t, err)

		rl, ok := middleware.(*rule.RateLimiter)
		require.True(t, ok)
		assert.Equal(t, rule.RateLimiterAlgoBucket, rl.Algo)
		assert.Equal(t, 50, rl.RequestLimit)
		assert.Equal(t, 30*time.Second, rl.WindowDuration.Duration)
	})
}

func TestNewRateLimiter(t *testing.T) {
	t.Run("default algorithm (window)", func(t *testing.T) {
		r := rule.NewRateLimiter(50, 30*time.Second)

		assert.Equal(t, "RateLimiter", r.Type)

		args, ok := r.Args.(rule.RateLimiter)
		require.True(t, ok)
		assert.Equal(t, 50, args.RequestLimit)
		assert.Equal(t, 30*time.Second, args.WindowDuration.Duration)
		assert.Equal(t, rule.RateLimiterAlgo(""), args.Algo) // empty string, defaults to window
	})

	t.Run("bucket algorithm", func(t *testing.T) {
		r := rule.NewRateLimiter(100, time.Minute, rule.RateLimiterAlgoBucket)

		assert.Equal(t, "RateLimiter", r.Type)

		args, ok := r.Args.(rule.RateLimiter)
		require.True(t, ok)
		assert.Equal(t, 100, args.RequestLimit)
		assert.Equal(t, time.Minute, args.WindowDuration.Duration)
		assert.Equal(t, rule.RateLimiterAlgoBucket, args.Algo)
	})
}

func BenchmarkRateLimiterProcess(b *testing.B) {
	rl := &rule.RateLimiter{
		RequestLimit:   1000000,
		WindowDuration: rule.WindowDuration{Duration: time.Minute},
	}
	rl.UpdateMiddleware(nil)

	handler := rl.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(rr, req)
	}
}
