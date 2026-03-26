package rate

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

// BucketCounter is the interface for token bucket storage.
type BucketCounter interface {
	// Take attempts to take n tokens from the bucket identified by key.
	// Returns the number of tokens remaining and whether the tokens were taken.
	Take(key string, n int) (remaining int, ok bool)
	// Config configures the bucket with capacity and refill rate.
	Config(capacity int, refillRate float64)
}

func NewBucketRateLimiter(capacity int, refillDuration time.Duration, options ...BucketOption) *bucketRateLimiter {
	return newBucketRateLimiter(capacity, refillDuration, options...)
}

func newBucketRateLimiter(capacity int, refillDuration time.Duration, options ...BucketOption) *bucketRateLimiter {
	// refillRate is tokens per second to refill to reach capacity over refillDuration
	refillRate := float64(capacity) / refillDuration.Seconds()

	rl := &bucketRateLimiter{
		capacity:       capacity,
		refillRate:     refillRate,
		refillDuration: refillDuration,
	}

	for _, opt := range options {
		opt(rl)
	}

	if rl.keyFn == nil {
		rl.keyFn = func(r *http.Request) (string, error) {
			return "*", nil
		}
	}

	if rl.bucketCounter == nil {
		rl.bucketCounter = &localBucketCounter{
			buckets:        make(map[string]*bucket),
			capacity:       capacity,
			refillRate:     refillRate,
			refillDuration: refillDuration,
		}
	}
	rl.bucketCounter.Config(capacity, refillRate)

	if rl.onRequestLimit == nil {
		rl.onRequestLimit = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		}
	}

	return rl
}

type BucketOption func(*bucketRateLimiter)

type bucketRateLimiter struct {
	capacity       int
	refillRate     float64
	refillDuration time.Duration
	keyFn          KeyFunc
	bucketCounter  BucketCounter
	onRequestLimit http.HandlerFunc
	mu             sync.Mutex
}

func (l *bucketRateLimiter) Counter() BucketCounter {
	return l.bucketCounter
}

func (l *bucketRateLimiter) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := l.keyFn(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusPreconditionRequired)
			return
		}

		l.mu.Lock()
		remaining, ok := l.bucketCounter.Take(key, getIncrement(r.Context()))
		l.mu.Unlock()

		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", l.capacity))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(l.refillDuration).Unix()))

		if !ok {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(l.refillDuration.Seconds())))
			l.onRequestLimit(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type bucket struct {
	tokens    float64
	updatedAt time.Time
}

type localBucketCounter struct {
	buckets        map[string]*bucket
	capacity       int
	refillRate     float64
	refillDuration time.Duration
	lastEvict      time.Time
	mu             sync.Mutex
}

var _ BucketCounter = &localBucketCounter{}

func (c *localBucketCounter) Config(capacity int, refillRate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.capacity = capacity
	c.refillRate = refillRate
}

func (c *localBucketCounter) Take(key string, n int) (remaining int, ok bool) {
	c.evict()

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	b, exists := c.buckets[key]
	if !exists {
		b = &bucket{
			tokens:    float64(c.capacity),
			updatedAt: now,
		}
		c.buckets[key] = b
	}

	// Refill tokens based on time elapsed
	elapsed := now.Sub(b.updatedAt).Seconds()
	b.tokens = math.Min(float64(c.capacity), b.tokens+elapsed*c.refillRate)
	b.updatedAt = now

	// Check if we have enough tokens
	if b.tokens < float64(n) {
		return int(b.tokens), false
	}

	// Take the tokens
	b.tokens -= float64(n)
	return int(b.tokens), true
}

func (c *localBucketCounter) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()

	d := c.refillDuration * 3

	if time.Since(c.lastEvict) < d {
		return
	}
	c.lastEvict = time.Now()

	for k, v := range c.buckets {
		if time.Since(v.updatedAt) >= d {
			delete(c.buckets, k)
		}
	}
}

// BucketLimit creates a new rate limiter middleware using token bucket algorithm.
func BucketLimit(capacity int, refillDuration time.Duration, options ...BucketOption) func(next http.Handler) http.Handler {
	return NewBucketRateLimiter(capacity, refillDuration, options...).Handler
}

// BucketLimitByIP creates a rate limiter using token bucket algorithm keyed by IP.
func BucketLimitByIP(capacity int, refillDuration time.Duration) func(next http.Handler) http.Handler {
	return BucketLimit(capacity, refillDuration, WithBucketKeyFuncs(KeyByIP))
}

// BucketLimitByRealIP creates a rate limiter using token bucket algorithm keyed by real IP.
func BucketLimitByRealIP(capacity int, refillDuration time.Duration) func(next http.Handler) http.Handler {
	return BucketLimit(capacity, refillDuration, WithBucketKeyFuncs(KeyByRealIP))
}

// WithBucketKeyFuncs sets the key functions for the bucket rate limiter.
func WithBucketKeyFuncs(keyFuncs ...KeyFunc) BucketOption {
	return func(rl *bucketRateLimiter) {
		if len(keyFuncs) > 0 {
			rl.keyFn = composedKeyFunc(keyFuncs...)
		}
	}
}

// WithBucketLimitHandler sets the handler called when rate limit is exceeded.
func WithBucketLimitHandler(h http.HandlerFunc) BucketOption {
	return func(rl *bucketRateLimiter) {
		rl.onRequestLimit = h
	}
}

// WithBucketCounter sets a custom bucket counter implementation.
func WithBucketCounter(c BucketCounter) BucketOption {
	return func(rl *bucketRateLimiter) {
		rl.bucketCounter = c
	}
}
