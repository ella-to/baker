package rate

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBucketLimit(t *testing.T) {
	tests := []struct {
		name         string
		capacity     int
		refillDur    time.Duration
		reqCount     int
		expectBlocks bool
	}{
		{
			name:         "no-block",
			capacity:     5,
			refillDur:    time.Minute,
			reqCount:     5,
			expectBlocks: false,
		},
		{
			name:         "block",
			capacity:     5,
			refillDur:    time.Minute,
			reqCount:     6,
			expectBlocks: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := BucketLimit(tt.capacity, tt.refillDur)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			blocked := false
			for i := 0; i < tt.reqCount; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				if rr.Code == http.StatusTooManyRequests {
					blocked = true
				}
			}

			if tt.expectBlocks != blocked {
				t.Errorf("expected blocked=%v, got blocked=%v", tt.expectBlocks, blocked)
			}
		})
	}
}

func TestBucketLimitByIP(t *testing.T) {
	tests := []struct {
		name         string
		capacity     int
		refillDur    time.Duration
		ip           string
		reqCount     int
		expectBlocks bool
	}{
		{
			name:         "no-block",
			capacity:     5,
			refillDur:    time.Minute,
			ip:           "127.0.0.1:1234",
			reqCount:     5,
			expectBlocks: false,
		},
		{
			name:         "block-ip",
			capacity:     5,
			refillDur:    time.Minute,
			ip:           "127.0.0.2:1234",
			reqCount:     6,
			expectBlocks: true,
		},
		{
			name:         "block-ipv6",
			capacity:     5,
			refillDur:    time.Minute,
			ip:           "[2001:db8::1]:1234",
			reqCount:     6,
			expectBlocks: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := BucketLimitByIP(tt.capacity, tt.refillDur)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			blocked := false
			for i := 0; i < tt.reqCount; i++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = tt.ip
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				if rr.Code == http.StatusTooManyRequests {
					blocked = true
				}
			}

			if tt.expectBlocks != blocked {
				t.Errorf("expected blocked=%v, got blocked=%v", tt.expectBlocks, blocked)
			}
		})
	}
}

func TestBucketRefill(t *testing.T) {
	// Test that tokens refill over time
	counter := &localBucketCounter{
		buckets:        make(map[string]*bucket),
		capacity:       10,
		refillRate:     10.0, // 10 tokens per second
		refillDuration: time.Second,
	}
	counter.Config(10, 10.0)

	// Take all tokens
	remaining, ok := counter.Take("test", 10)
	if !ok {
		t.Error("should be able to take 10 tokens")
	}
	if remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", remaining)
	}

	// Should not be able to take more
	_, ok = counter.Take("test", 1)
	if ok {
		t.Error("should not be able to take tokens when bucket is empty")
	}

	// Wait for refill (100ms should give us ~1 token)
	time.Sleep(150 * time.Millisecond)

	// Should be able to take 1 token now
	_, ok = counter.Take("test", 1)
	if !ok {
		t.Error("should be able to take 1 token after refill")
	}
}

func TestBucketLimitHandler(t *testing.T) {
	customHandlerCalled := false
	handler := BucketLimit(1, time.Minute, WithBucketLimitHandler(func(w http.ResponseWriter, r *http.Request) {
		customHandlerCalled = true
		w.WriteHeader(http.StatusServiceUnavailable)
	}))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should succeed
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Second request should trigger custom handler
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
	if !customHandlerCalled {
		t.Error("custom handler should have been called")
	}
}
