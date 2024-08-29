package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var websocketRequestCount = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "baker",
		Name:      "websocket_request_count",
		Help:      "How many WebSocket requests processed, partitioned by status code, method and HTTP path.",
	},
	[]string{"domain", "path", "method", "code"},
)

var httpRequestCount = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "baker",
		Name:      "http_request_count",
		Help:      "How many HTTP requests processed, partitioned by status code, method and HTTP path (with patterns).",
	},
	[]string{"domain", "path", "method", "code"},
)

var httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "baker",
	Name:      "http_request_duration_seconds",
	Help:      "How long it took to process the request, partitioned by status code, method and HTTP path (with patterns).",
	Buckets:   []float64{.1, .3, 1, 1.5, 2, 5, 10},
},
	[]string{"domain", "path", "method", "code"},
)

var infoGuage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "baker",
	Name:      "info",
	Help:      "Information about the baker version and commit hash",
}, []string{"version", "commit"})

func SetInfo(version, commit string) {
	infoGuage.With(prometheus.Labels{
		"version": version,
		"commit":  commit,
	}).Set(1)
}

func HttpRequestCount(domain string, method string, path string, code int) {
	httpRequestCount.With(prometheus.Labels{
		"domain": domain,
		"method": method,
		"path":   path,
		"code":   strconv.FormatInt(int64(code), 10),
	}).Inc()
}

func HttpRequestDuration(domain string, method string, path string, code int, duration float64) {
	httpRequestDuration.With(prometheus.Labels{
		"domain": domain,
		"method": method,
		"path":   path,
		"code":   strconv.FormatInt(int64(code), 10),
	}).Observe(duration)
}

func WebsocketRequest(domain string, method string, path string, code int) {
	websocketRequestCount.With(prometheus.Labels{
		"domain": domain,
		"method": method,
		"path":   path,
		"code":   strconv.FormatInt(int64(code), 10),
	}).Inc()
}

func SetupHandler() http.Handler {
	req := prometheus.NewRegistry()

	req.MustRegister(
		collectors.NewGoCollector(),
		infoGuage,
		httpRequestCount,
		httpRequestDuration,
		websocketRequestCount,
	)

	// Create a custom http serve mux
	//

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(req, promhttp.HandlerOpts{}))

	return mux
}
