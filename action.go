package baker

import (
	"context"
	"sync"
)

// ActionRunner adapts the driver-facing mutation API (Add/Remove/Update) and the
// read API (Get/HasDomain) onto callbacks supplied by the server.
//
// It used to serialize every operation through a single goroutine and channel,
// which made the request read path a throughput bottleneck and silently dropped
// events when the channel filled. Synchronization is now provided by the
// server's RWMutex inside the callbacks, so these methods invoke the callbacks
// directly: mutations block briefly on the write lock (and are therefore visible
// the instant the call returns), while reads run concurrently under the read
// lock.
type ActionRunner struct {
	pingerCallback    func()
	addCallback       func(*Container)
	updateCallback    func(*Container, *Endpoint)
	removeCallback    func(*Container)
	getCallback       func(string, string) (*Container, *Endpoint)
	hasDomainCallback func(string) bool

	closeOnce sync.Once
	close     chan struct{}
}

var _ Driver = (*ActionRunner)(nil)

func (ar *ActionRunner) Pinger() {
	ar.pingerCallback()
}

func (ar *ActionRunner) Add(container *Container) {
	ar.addCallback(container)
}

func (ar *ActionRunner) Update(container *Container, endpoint *Endpoint) {
	ar.updateCallback(container, endpoint)
}

func (ar *ActionRunner) Remove(container *Container) {
	ar.removeCallback(container)
}

func (ar *ActionRunner) Get(ctx context.Context, endpoint *Endpoint) (*Container, *Endpoint) {
	return ar.getCallback(endpoint.Domain, endpoint.Path)
}

func (ar *ActionRunner) HasDomain(ctx context.Context, domain string) bool {
	return ar.hasDomainCallback(domain)
}

func (ar *ActionRunner) Close() {
	ar.closeOnce.Do(func() {
		close(ar.close)
	})
}

func WithPingerCallback(callback func()) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.pingerCallback = callback
	}
}

func WithAddCallback(callback func(*Container)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.addCallback = callback
	}
}

func WithUpdateCallback(callback func(*Container, *Endpoint)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.updateCallback = callback
	}
}

func WithRemoveCallback(callback func(*Container)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.removeCallback = callback
	}
}

func WithGetCallback(callback func(string, string) (*Container, *Endpoint)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.getCallback = callback
	}
}

func WithHasDomainCallback(callback func(string) bool) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.hasDomainCallback = callback
	}
}

type ActionCallback func(*ActionRunner)

// NewActionRunner builds an ActionRunner from the supplied callbacks. bufferSize
// is retained for backwards compatibility but is no longer used (operations are
// no longer queued through a channel).
func NewActionRunner(bufferSize int, cbs ...ActionCallback) *ActionRunner {
	ar := &ActionRunner{
		close: make(chan struct{}),
	}

	for _, cb := range cbs {
		cb(ar)
	}

	return ar
}
