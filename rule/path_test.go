package rule_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ella.to/baker/rule"
)

func TestAppendPath(t *testing.T) {
	t.Run("append path with begin and end", func(t *testing.T) {
		ap := &rule.AppendPath{
			Begin: "/prefix",
			End:   "/suffix",
		}

		handler := ap.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/prefix/original/path/suffix", r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/original/path", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("append path with begin only", func(t *testing.T) {
		ap := &rule.AppendPath{
			Begin: "/api/v1",
			End:   "",
		}

		handler := ap.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/users", r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/users", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("is not cachable", func(t *testing.T) {
		ap := &rule.AppendPath{}
		assert.False(t, ap.IsCachable())
	})

	t.Run("update middleware returns nil", func(t *testing.T) {
		ap := &rule.AppendPath{}
		assert.Nil(t, ap.UpdateMiddleware(nil))
	})
}

func TestReplacePath(t *testing.T) {
	t.Run("replace path single occurrence", func(t *testing.T) {
		rp := &rule.ReplacePath{
			Search:  "/api",
			Replace: "/v2",
			Times:   1,
		}

		handler := rp.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v2/users", r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("is not cachable", func(t *testing.T) {
		rp := &rule.ReplacePath{}
		assert.False(t, rp.IsCachable())
	})
}

func TestRegisterAppendPath(t *testing.T) {
	m := make(map[string]rule.BuilderFunc)
	err := rule.RegisterAppendPath()(m)
	require.NoError(t, err)

	builder, ok := m["AppendPath"]
	require.True(t, ok)

	middleware, err := builder(json.RawMessage(`{"begin":"/prefix","end":"/suffix"}`))
	require.NoError(t, err)

	ap, ok := middleware.(*rule.AppendPath)
	require.True(t, ok)
	assert.Equal(t, "/prefix", ap.Begin)
	assert.Equal(t, "/suffix", ap.End)
}

func TestRegisterReplacePath(t *testing.T) {
	m := make(map[string]rule.BuilderFunc)
	err := rule.RegisterReplacePath()(m)
	require.NoError(t, err)

	builder, ok := m["ReplacePath"]
	require.True(t, ok)

	middleware, err := builder(json.RawMessage(`{"search":"/old","replace":"/new","times":1}`))
	require.NoError(t, err)

	rp, ok := middleware.(*rule.ReplacePath)
	require.True(t, ok)
	assert.Equal(t, "/old", rp.Search)
	assert.Equal(t, "/new", rp.Replace)
	assert.Equal(t, 1, rp.Times)
}

func TestNewAppendPath(t *testing.T) {
	r := rule.NewAppendPath("/begin", "/end")

	assert.Equal(t, "AppendPath", r.Type)

	args, ok := r.Args.(rule.AppendPath)
	require.True(t, ok)
	assert.Equal(t, "/begin", args.Begin)
	assert.Equal(t, "/end", args.End)
}

func TestNewReplacePath(t *testing.T) {
	r := rule.NewReplacePath("/search", "/replace", 5)

	assert.Equal(t, "ReplacePath", r.Type)

	args, ok := r.Args.(rule.ReplacePath)
	require.True(t, ok)
	assert.Equal(t, "/search", args.Search)
	assert.Equal(t, "/replace", args.Replace)
	assert.Equal(t, 5, args.Times)
}

func TestChain(t *testing.T) {
	t.Run("chain multiple middlewares", func(t *testing.T) {
		middleware1 := &rule.AppendPath{Begin: "/1"}
		middleware2 := &rule.AppendPath{Begin: "/2"}

		handler := rule.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/2/1/original", r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}), middleware1, middleware2)

		req := httptest.NewRequest(http.MethodGet, "/original", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("empty chain", func(t *testing.T) {
		handled := false
		handler := rule.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handled = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.True(t, handled)
		assert.Equal(t, http.StatusOK, rr.Code)
	})
}

func BenchmarkAppendPath(b *testing.B) {
	ap := &rule.AppendPath{
		Begin: "/api/v1",
		End:   ".json",
	}

	handler := ap.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	rr := httptest.NewRecorder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(rr, req)
	}
}

func BenchmarkReplacePath(b *testing.B) {
	rp := &rule.ReplacePath{
		Search:  "/api",
		Replace: "/v2",
		Times:   1,
	}

	handler := rp.Process(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/users/api/data", nil)
	rr := httptest.NewRecorder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(rr, req)
	}
}
