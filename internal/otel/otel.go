// Package otel provides OTel instrumentation for ella.to/baker without
// touching the baker package itself. Wrap a baker server with NewHandler
// and every inbound request produces a span (kind=Server) and three RED
// metrics (request count, error count, duration histogram).
//
//	srv := baker.NewServer(...)
//	srv.RegisterDriver(docker.RegisterDriver)
//	handler := otel.NewHandler(srv)
//	http.ListenAndServe(":80", handler)
//
// Because baker is an HTTP reverse proxy, NewHandler also surfaces the
// active trace id as the X-Trace-Id response header so callers can pivot
// from a request to its Tempo trace, and uses WithPublicEndpoint by
// default since baker is the edge of the system.
package otel

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"ella.to/baker"
	otelella "ella.to/otel"
	otelconfig "ella.to/otel/config"
	otelhttp "ella.to/otel/http"
)

const (
	meterName = "ella.to/baker"
)

func Init(ctx context.Context, version string) func() {
	otelConfig := otelconfig.FromEnv()
	otelConfig.ServiceName = "baker"
	otelConfig.ServiceVersion = version

	otelShutdown, err := otelella.Init(ctx, otelConfig)
	if err != nil {
		slog.ErrorContext(ctx, "failed to initialize OpenTelemetry", "error", err)
		os.Exit(1)
	}

	return func() {
		if err = otelShutdown(context.WithoutCancel(ctx)); err != nil {
			slog.ErrorContext(ctx, "failed to shutdown OpenTelemetry", "error", err)
		}
	}
}

type handlerOptions struct {
	operation       string
	publicEndpoint  bool
	skipMetrics     bool
	skipHeader      bool
	extraOptionsFns []otelhttp.HandlerOption
}

// Option configures NewHandler.
type Option func(*handlerOptions)

// WithOperation overrides the operation name used as the default span
// name. Defaults to "baker.proxy".
func WithOperation(name string) Option {
	return func(o *handlerOptions) { o.operation = name }
}

// WithPublicEndpoint signals that this handler accepts traffic from
// untrusted callers. Default true.
func WithPublicEndpoint(public bool) Option {
	return func(o *handlerOptions) { o.publicEndpoint = public }
}

// WithoutMetrics disables the RED metrics emitted alongside spans. Useful
// when the caller already runs their own metrics middleware (for example
// the existing baker prometheus exporter).
func WithoutMetrics() Option {
	return func(o *handlerOptions) { o.skipMetrics = true }
}

// WithoutTraceIDHeader disables the X-Trace-Id response header.
func WithoutTraceIDHeader() Option {
	return func(o *handlerOptions) { o.skipHeader = true }
}

// NewHandler returns an http.Handler that wraps next with OTel tracing and
// metrics suitable for the baker reverse proxy edge.
func NewHandler(next http.Handler, opts ...Option) http.Handler {
	o := &handlerOptions{
		operation:      "baker.proxy",
		publicEndpoint: true,
	}
	for _, fn := range opts {
		fn(o)
	}

	h := next
	if !o.skipMetrics {
		h = newMetricsMiddleware(h)
	}

	httpOpts := []otelhttp.HandlerOption{
		otelhttp.WithOperation(o.operation),
	}
	if o.publicEndpoint {
		httpOpts = append(httpOpts, otelhttp.WithPublicEndpoint())
	}
	if o.skipHeader {
		httpOpts = append(httpOpts, otelhttp.WithoutTraceIDHeader())
	}
	httpOpts = append(httpOpts, o.extraOptionsFns...)

	return otelhttp.NewHandler(h, httpOpts...)
}

// metricsMiddleware emits RED metrics for every request flowing through
// the baker proxy, using the OTel global meter provider.
type metricsMiddleware struct {
	next     http.Handler
	requests metric.Int64Counter
	errors   metric.Int64Counter
	duration metric.Float64Histogram
}

func newMetricsMiddleware(next http.Handler) *metricsMiddleware {
	meter := otel.Meter(meterName)

	requests, err := meter.Int64Counter(
		"baker.http.requests",
		metric.WithDescription("Number of HTTP requests handled by baker."),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		panic(err)
	}
	errors, err := meter.Int64Counter(
		"baker.http.errors",
		metric.WithDescription("Number of HTTP requests with status >= 500."),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		panic(err)
	}
	duration, err := meter.Float64Histogram(
		"baker.http.duration",
		metric.WithDescription("Duration of HTTP requests handled by baker."),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	return &metricsMiddleware{next: next, requests: requests, errors: errors, duration: duration}
}

func (m *metricsMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	// Carry a RouteInfo so baker can report which registered route handled the
	// request. Using the matched route pattern (not the raw URL path) as the
	// metric attribute keeps time-series cardinality bounded by the number of
	// registered routes instead of growing without bound across distinct URLs.
	ctx, routeInfo := baker.WithRouteInfo(r.Context())
	m.next.ServeHTTP(rec, r.WithContext(ctx))

	route := routeInfo.Pattern
	if route == "" {
		route = "unmatched"
	}

	attrs := metric.WithAttributes(
		attribute.String("http.host", r.Host),
		attribute.String("http.method", r.Method),
		attribute.String("http.route", route),
		attribute.String("http.status_code", strconv.Itoa(rec.status)),
	)

	m.requests.Add(ctx, 1, attrs)
	if rec.status >= 500 {
		m.errors.Add(ctx, 1, attrs)
	}
	m.duration.Record(ctx, time.Since(start).Seconds(), attrs)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// Hijack is required so the wrapped handler stays compatible with baker's
// WebSocket upgrade path. Returning an error rather than panicking lets
// callers fail gracefully when the underlying writer does not support it.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("otelbaker: response writer does not support hijacking")
	}
	return hj.Hijack()
}

// Flush forwards to the underlying writer when supported, so streaming
// responses (SSE, chunked transfer) flush promptly.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
