package rule

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"ella.to/baker/rule/internal/rate"
)

// RateLimiterAlgo defines the algorithm used for rate limiting.
type RateLimiterAlgo string

const (
	// RateLimiterAlgoWindow uses a sliding window algorithm (default).
	RateLimiterAlgoWindow RateLimiterAlgo = "window"
	// RateLimiterAlgoBucket uses a token bucket algorithm.
	RateLimiterAlgoBucket RateLimiterAlgo = "bucket"
)

type WindowDuration struct {
	time.Duration
}

// MarshalJSON implements the json.Marshaler interface for WindowDuration.
func (d WindowDuration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%s"`, d.String())), nil
}

// UnmarshalJSON implements the json.Unmarshaler interface for WindowDuration.
func (d *WindowDuration) UnmarshalJSON(data []byte) error {
	if len(data) < 2 {
		d.Duration = 0
		return nil
	}

	duration, err := time.ParseDuration(string(data[1 : len(data)-1]))
	if err != nil {
		return err
	}

	d.Duration = duration
	return nil
}

type RateLimiter struct {
	Algo           RateLimiterAlgo `json:"algo,omitempty"`
	RequestLimit   int             `json:"request_limit"`
	WindowDuration WindowDuration  `json:"window_duration"`
	middle         func(next http.Handler) http.Handler
}

var _ Middleware = (*RateLimiter)(nil)

func (r *RateLimiter) IsCachable() bool {
	return true
}

// getAlgo returns the algorithm to use, defaulting to window if not specified.
func (r *RateLimiter) getAlgo() RateLimiterAlgo {
	if r.Algo == "" {
		return RateLimiterAlgoWindow
	}
	return r.Algo
}

// createMiddleware creates the appropriate rate limiter middleware based on the algorithm.
func (r *RateLimiter) createMiddleware() func(next http.Handler) http.Handler {
	switch r.getAlgo() {
	case RateLimiterAlgoBucket:
		return rate.BucketLimitByIP(r.RequestLimit, r.WindowDuration.Duration)
	default:
		return rate.LimitByIP(r.RequestLimit, r.WindowDuration.Duration)
	}
}

func (r *RateLimiter) UpdateMiddleware(newImpl Middleware) Middleware {
	if newImpl == nil {
		slog.Debug(
			"initializing for the first time",
			"type", "RateLimiter",
			"algo", r.getAlgo(),
			"request_limit", r.RequestLimit,
			"window_duration", r.WindowDuration.Duration,
		)

		r.middle = r.createMiddleware()
		return r
	}

	newR, ok := newImpl.(*RateLimiter)
	if !ok {
		slog.Error("failed to update middleware", "type", "RateLimiter")
		return r
	}

	if r.RequestLimit == newR.RequestLimit &&
		r.WindowDuration == newR.WindowDuration &&
		r.getAlgo() == newR.getAlgo() &&
		r.middle != nil {
		return r
	}

	slog.Debug(
		"updating middleware",
		"type", "RateLimiter",
		"algo", newR.getAlgo(),
		"request_limit", newR.RequestLimit,
		"window_duration", newR.WindowDuration.Duration,
	)

	r.Algo = newR.Algo
	r.RequestLimit = newR.RequestLimit
	r.WindowDuration = newR.WindowDuration

	r.middle = r.createMiddleware()

	return r
}

func (r *RateLimiter) Process(next http.Handler) http.Handler {
	return r.middle(next)
}

func NewRateLimiter(requestLimit int, windowDuration time.Duration, algo ...RateLimiterAlgo) struct {
	Type string `json:"type"`
	Args any    `json:"args"`
} {
	var a RateLimiterAlgo
	if len(algo) > 0 && algo[0] != RateLimiterAlgoWindow {
		a = algo[0]
	}
	return struct {
		Type string `json:"type"`
		Args any    `json:"args"`
	}{
		Type: "RateLimiter",
		Args: RateLimiter{
			Algo:         a,
			RequestLimit: requestLimit,
			WindowDuration: WindowDuration{
				Duration: windowDuration,
			},
		},
	}
}

func RegisterRateLimiter() RegisterFunc {
	return func(m map[string]BuilderFunc) error {
		m["RateLimiter"] = func(raw json.RawMessage) (Middleware, error) {
			rateLimiter := &RateLimiter{}
			err := json.Unmarshal(raw, rateLimiter)
			if err != nil {
				return nil, err
			}
			return rateLimiter, nil
		}

		return nil
	}
}
