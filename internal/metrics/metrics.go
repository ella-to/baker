package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var processedRequests = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "baker",
		Name:      "baker_pattern_requests_total",
		Help:      "How many HTTP requests processed, partitioned by status code, method and HTTP path (with patterns).",
	},
	[]string{"domain", "path", "method", "code"},
)

var requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "baker",
	Name:      "baker_pattern_request_duration_seconds",
	Help:      "How long it took to process the request, partitioned by status code, method and HTTP path (with patterns).",
	Buckets:   []float64{.1, .3, 1, 1.5, 2, 5, 10},
},
	[]string{"domain", "path", "method", "code"},
)

var infoGuage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "bus",
	Name:      "info",
	Help:      "Information about the bus version and commit hash",
}, []string{"version", "commit"})

func SetInfo(version, commit string) {
	infoGuage.With(prometheus.Labels{
		"version": version,
		"commit":  commit,
	}).Set(1)
}

func HttpProcessedRequest(domain string, method string, path string, code int) {
	processedRequests.With(prometheus.Labels{
		"domain": domain,
		"method": method,
		"path":   path,
		"code":   strconv.FormatInt(int64(code), 10),
	}).Inc()
}

func HttpRequestDuration(domain string, method string, path string, code int, duration float64) {
	requestDuration.With(prometheus.Labels{
		"domain": domain,
		"method": method,
		"path":   path,
		"code":   strconv.FormatInt(int64(code), 10),
	}).Observe(duration)
}

func SetupHandler() http.Handler {
	req := prometheus.NewRegistry()

	req.MustRegister(
		collectors.NewGoCollector(),
		infoGuage,
		processedRequests,
		requestDuration,
	)

	// Create a custom http serve mux
	//

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(req, promhttp.HandlerOpts{}))

	return mux
}
