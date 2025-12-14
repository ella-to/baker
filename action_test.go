package baker_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"ella.to/baker"
)

func TestActionRunner(t *testing.T) {
	t.Run("Add callback is called", func(t *testing.T) {
		var addedContainer *baker.Container
		var mu sync.Mutex

		runner := baker.NewActionRunner(
			100,
			baker.WithAddCallback(func(c *baker.Container) {
				mu.Lock()
				addedContainer = c
				mu.Unlock()
			}),
			baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {}),
			baker.WithRemoveCallback(func(c *baker.Container) {}),
			baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) { return nil, nil }),
			baker.WithPingerCallback(func() {}),
		)

		container := &baker.Container{Id: "test-container-1"}
		runner.Add(container)

		time.Sleep(50 * time.Millisecond)
		runner.Close()

		mu.Lock()
		assert.NotNil(t, addedContainer)
		assert.Equal(t, "test-container-1", addedContainer.Id)
		mu.Unlock()
	})

	t.Run("Update callback is called", func(t *testing.T) {
		var updatedEndpoint *baker.Endpoint
		var mu sync.Mutex

		runner := baker.NewActionRunner(
			100,
			baker.WithAddCallback(func(c *baker.Container) {}),
			baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {
				mu.Lock()
				updatedEndpoint = e
				mu.Unlock()
			}),
			baker.WithRemoveCallback(func(c *baker.Container) {}),
			baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) { return nil, nil }),
			baker.WithPingerCallback(func() {}),
		)

		container := &baker.Container{Id: "test-container-2"}
		endpoint := &baker.Endpoint{Domain: "example.com", Path: "/api"}
		runner.Update(container, endpoint)

		time.Sleep(50 * time.Millisecond)
		runner.Close()

		mu.Lock()
		assert.NotNil(t, updatedEndpoint)
		assert.Equal(t, "example.com", updatedEndpoint.Domain)
		assert.Equal(t, "/api", updatedEndpoint.Path)
		mu.Unlock()
	})

	t.Run("Remove callback is called", func(t *testing.T) {
		var removedContainer *baker.Container
		var mu sync.Mutex

		runner := baker.NewActionRunner(
			100,
			baker.WithAddCallback(func(c *baker.Container) {}),
			baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {}),
			baker.WithRemoveCallback(func(c *baker.Container) {
				mu.Lock()
				removedContainer = c
				mu.Unlock()
			}),
			baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) { return nil, nil }),
			baker.WithPingerCallback(func() {}),
		)

		container := &baker.Container{Id: "test-container-3"}
		runner.Remove(container)

		time.Sleep(50 * time.Millisecond)
		runner.Close()

		mu.Lock()
		assert.NotNil(t, removedContainer)
		assert.Equal(t, "test-container-3", removedContainer.Id)
		mu.Unlock()
	})

	t.Run("Get returns container", func(t *testing.T) {
		expectedContainer := &baker.Container{
			Id:   "test-container-4",
			Addr: netip.MustParseAddrPort("127.0.0.1:8080"),
		}
		expectedEndpoint := &baker.Endpoint{Domain: "get.example.com", Path: "/test"}

		runner := baker.NewActionRunner(
			100,
			baker.WithAddCallback(func(c *baker.Container) {}),
			baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {}),
			baker.WithRemoveCallback(func(c *baker.Container) {}),
			baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) {
				if domain == "get.example.com" && path == "/test" {
					return expectedContainer, expectedEndpoint
				}
				return nil, nil
			}),
			baker.WithPingerCallback(func() {}),
		)

		ctx := context.Background()
		queryEndpoint := &baker.Endpoint{Domain: "get.example.com", Path: "/test"}
		container, endpoint := runner.Get(ctx, queryEndpoint)

		assert.NotNil(t, container)
		assert.Equal(t, "test-container-4", container.Id)
		assert.NotNil(t, endpoint)
		assert.Equal(t, "get.example.com", endpoint.Domain)

		runner.Close()
	})

	t.Run("Get returns nil for unknown domain", func(t *testing.T) {
		runner := baker.NewActionRunner(
			100,
			baker.WithAddCallback(func(c *baker.Container) {}),
			baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {}),
			baker.WithRemoveCallback(func(c *baker.Container) {}),
			baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) {
				return nil, nil
			}),
			baker.WithPingerCallback(func() {}),
		)

		ctx := context.Background()
		queryEndpoint := &baker.Endpoint{Domain: "unknown.com", Path: "/unknown"}
		container, endpoint := runner.Get(ctx, queryEndpoint)

		assert.Nil(t, container)
		assert.Nil(t, endpoint)

		runner.Close()
	})

	t.Run("Pinger callback is called", func(t *testing.T) {
		pingerCalled := false
		var mu sync.Mutex

		runner := baker.NewActionRunner(
			100,
			baker.WithAddCallback(func(c *baker.Container) {}),
			baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {}),
			baker.WithRemoveCallback(func(c *baker.Container) {}),
			baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) { return nil, nil }),
			baker.WithPingerCallback(func() {
				mu.Lock()
				pingerCalled = true
				mu.Unlock()
			}),
		)

		runner.Pinger()

		time.Sleep(50 * time.Millisecond)
		runner.Close()

		mu.Lock()
		assert.True(t, pingerCalled)
		mu.Unlock()
	})
}

func BenchmarkActionRunnerAdd(b *testing.B) {
	runner := baker.NewActionRunner(
		10000,
		baker.WithAddCallback(func(c *baker.Container) {}),
		baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {}),
		baker.WithRemoveCallback(func(c *baker.Container) {}),
		baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) { return nil, nil }),
		baker.WithPingerCallback(func() {}),
	)

	container := &baker.Container{Id: "bench-container"}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		runner.Add(container)
	}

	runner.Close()
}

func BenchmarkActionRunnerGet(b *testing.B) {
	expectedContainer := &baker.Container{Id: "bench-container"}
	expectedEndpoint := &baker.Endpoint{Domain: "bench.example.com", Path: "/"}

	runner := baker.NewActionRunner(
		10000,
		baker.WithAddCallback(func(c *baker.Container) {}),
		baker.WithUpdateCallback(func(c *baker.Container, e *baker.Endpoint) {}),
		baker.WithRemoveCallback(func(c *baker.Container) {}),
		baker.WithGetCallback(func(domain, path string) (*baker.Container, *baker.Endpoint) {
			return expectedContainer, expectedEndpoint
		}),
		baker.WithPingerCallback(func() {}),
	)

	ctx := context.Background()
	queryEndpoint := &baker.Endpoint{Domain: "bench.example.com", Path: "/"}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		runner.Get(ctx, queryEndpoint)
	}

	runner.Close()
}
