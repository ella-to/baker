package httpclient_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ella.to/baker/internal/httpclient"
)

func TestClientGet(t *testing.T) {
	t.Run("successful GET request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/test/path", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message": "success"}`))
		}))
		defer server.Close()

		client, err := httpclient.NewClient(httpclient.WithHttpClientTimeout(5*time.Second, server.URL))
		require.NoError(t, err)

		body, statusCode, err := client.Get(context.Background(), "/test/path")
		require.NoError(t, err)
		defer body.Close()

		assert.Equal(t, http.StatusOK, statusCode)

		content, err := io.ReadAll(body)
		require.NoError(t, err)
		assert.Equal(t, `{"message": "success"}`, string(content))
	})

	t.Run("404 response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		}))
		defer server.Close()

		client, err := httpclient.NewClient(httpclient.WithHttpClientTimeout(5*time.Second, server.URL))
		require.NoError(t, err)

		body, statusCode, err := client.Get(context.Background(), "/missing")
		require.NoError(t, err)
		defer body.Close()

		assert.Equal(t, http.StatusNotFound, statusCode)
	})

	t.Run("default client", func(t *testing.T) {
		client, err := httpclient.NewClient()
		require.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("with custom http client", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("custom client"))
		}))
		defer server.Close()

		customClient := &http.Client{
			Timeout: 10 * time.Second,
		}

		client, err := httpclient.NewClient(httpclient.WithHttpClient(customClient, server.URL))
		require.NoError(t, err)

		body, statusCode, err := client.Get(context.Background(), "/test")
		require.NoError(t, err)
		defer body.Close()

		assert.Equal(t, http.StatusOK, statusCode)
	})
}

func TestGetterFunc(t *testing.T) {
	t.Run("getter func implements Getter interface", func(t *testing.T) {
		fn := httpclient.GetterFunc(func(ctx context.Context, url string) (io.ReadCloser, int, error) {
			return io.NopCloser(nil), http.StatusOK, nil
		})

		body, statusCode, err := fn.Get(context.Background(), "/test")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, body)
	})
}

func BenchmarkClientGet(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("benchmark response"))
	}))
	defer server.Close()

	client, _ := httpclient.NewClient(httpclient.WithHttpClientTimeout(5*time.Second, server.URL))
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		body, _, _ := client.Get(ctx, "/benchmark")
		if body != nil {
			body.Close()
		}
	}
}
