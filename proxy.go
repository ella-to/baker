package baker

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// proxyTarget carries the per-request backend selection into the shared
// ReverseProxy's Rewrite callback via the request context. This lets a single
// ReverseProxy (and its connection pool / buffer pool) serve every backend
// instead of allocating a new proxy per request.
type proxyTarget struct {
	host    string
	headers map[string]string
}

type proxyTargetKeyType struct{}

var proxyTargetKey proxyTargetKeyType

// bufferPool is a sync.Pool-backed httputil.BufferPool so the ReverseProxy
// reuses 32KiB copy buffers across requests instead of allocating one per
// proxied response.
type bufferPool struct {
	pool sync.Pool
}

func newBufferPool() *bufferPool {
	return &bufferPool{
		pool: sync.Pool{
			New: func() any {
				b := make([]byte, 32*1024)
				return &b
			},
		},
	}
}

func (b *bufferPool) Get() []byte  { return *(b.pool.Get().(*[]byte)) }
func (b *bufferPool) Put(p []byte) { b.pool.Put(&p) }

// newReverseProxy builds the single shared reverse proxy used for all HTTP
// (non-WebSocket) traffic. The backend is selected per request through the
// proxyTarget stashed in the request context.
func newReverseProxy() *httputil.ReverseProxy {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &httputil.ReverseProxy{
		Transport:  transport,
		BufferPool: newBufferPool(),
		Rewrite: func(pr *httputil.ProxyRequest) {
			t, _ := pr.In.Context().Value(proxyTargetKey).(*proxyTarget)
			if t == nil {
				return
			}

			out := &url.URL{Scheme: "http", Host: t.host}
			slog.Debug("rewriting url", "from", pr.In.URL.String(), "to", out.String())

			pr.SetURL(out)
			pr.SetXForwarded()

			for k, v := range t.headers {
				key := strings.ToUpper(k)
				if key == "HOST" {
					pr.Out.Host = v
					continue
				}
				pr.Out.Header.Set(key, v)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("proxy backend error", "error", err, "host", r.Host, "path", r.URL.Path)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

func withProxyTarget(ctx context.Context, t *proxyTarget) context.Context {
	return context.WithValue(ctx, proxyTargetKey, t)
}
