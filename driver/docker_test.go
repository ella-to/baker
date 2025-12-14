package driver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ella.to/baker"
	"ella.to/baker/driver"
	"ella.to/baker/internal/httpclient"
)

// mockGetter creates a mock HTTP getter for testing
func mockGetter(responses map[string]string) httpclient.Getter {
	return httpclient.GetterFunc(func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		if resp, ok := responses[url]; ok {
			return io.NopCloser(strings.NewReader(resp)), 200, nil
		}
		return io.NopCloser(strings.NewReader(`{"error": "not found"}`)), 404, nil
	})
}

// mockDriver implements baker.Driver for testing
type mockDriver struct {
	mu           sync.Mutex
	added        []*baker.Container
	removed      []*baker.Container
	addCalled    int
	removeCalled int
}

func (d *mockDriver) Add(c *baker.Container) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.added = append(d.added, c)
	d.addCalled++
}

func (d *mockDriver) Remove(c *baker.Container) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removed = append(d.removed, c)
	d.removeCalled++
}

func (d *mockDriver) getAdded() []*baker.Container {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]*baker.Container, len(d.added))
	copy(result, d.added)
	return result
}

func (d *mockDriver) getAddedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.addCalled
}

func TestDockerLoadCurrentContainers(t *testing.T) {
	t.Run("loads running containers", func(t *testing.T) {
		containersJSON := `[
			{"Id": "container1", "State": "running"},
			{"Id": "container2", "State": "exited"},
			{"Id": "container3", "State": "running"}
		]`

		container1JSON := `{
			"Id": "container1",
			"Config": {
				"Labels": {
					"baker.enable": "true",
					"baker.network": "baker_net",
					"baker.service.port": "8080",
					"baker.service.ping": "/health"
				}
			},
			"NetworkSettings": {
				"Networks": {
					"baker_net": {
						"IPAddress": "172.18.0.2"
					}
				}
			}
		}`

		container3JSON := `{
			"Id": "container3",
			"Config": {
				"Labels": {
					"baker.enable": "true",
					"baker.network": "baker_net",
					"baker.service.port": "9000",
					"baker.service.ping": "/config"
				}
			},
			"NetworkSettings": {
				"Networks": {
					"baker_net": {
						"IPAddress": "172.18.0.3"
					}
				}
			}
		}`

		getter := mockGetter(map[string]string{
			"/containers/json":            containersJSON,
			"/containers/container1/json": container1JSON,
			"/containers/container3/json": container3JSON,
		})

		d := driver.NewDocker(getter)
		mock := &mockDriver{}

		// This will trigger loading current containers
		d.RegisterDriver(mock)

		// Wait for async loading
		time.Sleep(100 * time.Millisecond)
		d.Close()

		// Should have loaded 2 running containers
		added := mock.getAdded()
		assert.Equal(t, 2, len(added))
	})

	t.Run("skips containers without baker.enable", func(t *testing.T) {
		containersJSON := `[{"Id": "container1", "State": "running"}]`

		container1JSON := `{
			"Id": "container1",
			"Config": {
				"Labels": {
					"baker.enable": "false",
					"baker.network": "baker_net"
				}
			},
			"NetworkSettings": {
				"Networks": {
					"baker_net": {"IPAddress": "172.18.0.2"}
				}
			}
		}`

		getter := mockGetter(map[string]string{
			"/containers/json":            containersJSON,
			"/containers/container1/json": container1JSON,
		})

		d := driver.NewDocker(getter)
		mock := &mockDriver{}
		d.RegisterDriver(mock)

		time.Sleep(100 * time.Millisecond)
		d.Close()

		assert.Equal(t, 0, mock.getAddedCount())
	})
}

func TestDockerParseLabels(t *testing.T) {
	t.Run("parses all labels correctly", func(t *testing.T) {
		containerJSON := `{
			"Id": "test-container",
			"Config": {
				"Labels": {
					"baker.enable": "true",
					"baker.network": "my_network",
					"baker.service.port": "3000",
					"baker.service.ping": "/api/health",
					"baker.service.static.domain": "example.com",
					"baker.service.static.path": "/*",
					"baker.service.static.headers.host": "example.com",
					"baker.service.static.headers.authorization": "Bearer token123"
				}
			},
			"NetworkSettings": {
				"Networks": {
					"my_network": {
						"IPAddress": "172.18.0.5"
					}
				}
			}
		}`

		containersJSON := `[{"Id": "test-container", "State": "running"}]`

		getter := mockGetter(map[string]string{
			"/containers/json":                containersJSON,
			"/containers/test-container/json": containerJSON,
		})

		d := driver.NewDocker(getter)
		mock := &mockDriver{}
		d.RegisterDriver(mock)

		time.Sleep(100 * time.Millisecond)
		d.Close()

		added := mock.getAdded()
		require.Equal(t, 1, len(added))

		c := added[0]
		assert.Equal(t, "test-container", c.Id)
		assert.Equal(t, "/api/health", c.ConfigPath)

		expectedAddr, _ := netip.ParseAddrPort("172.18.0.5:3000")
		assert.Equal(t, expectedAddr, c.Addr)

		assert.Equal(t, "example.com", c.Meta.Static.Domain)
		assert.Equal(t, "/*", c.Meta.Static.Path)
		assert.Equal(t, "example.com", c.Meta.Static.Headers["host"])
		assert.Equal(t, "Bearer token123", c.Meta.Static.Headers["authorization"])
	})

	t.Run("handles missing network", func(t *testing.T) {
		containerJSON := `{
			"Id": "test-container",
			"Config": {
				"Labels": {
					"baker.enable": "true",
					"baker.network": "nonexistent_network",
					"baker.service.port": "3000",
					"baker.service.ping": "/health"
				}
			},
			"NetworkSettings": {
				"Networks": {
					"other_network": {
						"IPAddress": "172.18.0.5"
					}
				}
			}
		}`

		containersJSON := `[{"Id": "test-container", "State": "running"}]`

		getter := mockGetter(map[string]string{
			"/containers/json":                containersJSON,
			"/containers/test-container/json": containerJSON,
		})

		d := driver.NewDocker(getter)
		mock := &mockDriver{}
		d.RegisterDriver(mock)

		time.Sleep(100 * time.Millisecond)
		d.Close()

		// Container should not be added due to missing network
		assert.Equal(t, 0, mock.getAddedCount())
	})
}

func TestDockerClose(t *testing.T) {
	getter := mockGetter(map[string]string{
		"/containers/json": `[]`,
	})

	d := driver.NewDocker(getter)
	mock := &mockDriver{}
	d.RegisterDriver(mock)

	// Should not panic when closing
	assert.NotPanics(t, func() {
		d.Close()
	})
}

// Benchmarks
func BenchmarkDockerContainerLoad(b *testing.B) {
	containersJSON := `[{"Id": "container1", "State": "running"}]`
	containerJSON := `{
		"Id": "container1",
		"Config": {
			"Labels": {
				"baker.enable": "true",
				"baker.network": "baker_net",
				"baker.service.port": "8080",
				"baker.service.ping": "/health"
			}
		},
		"NetworkSettings": {
			"Networks": {
				"baker_net": {"IPAddress": "172.18.0.2"}
			}
		}
	}`

	getter := mockGetter(map[string]string{
		"/containers/json":            containersJSON,
		"/containers/container1/json": containerJSON,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d := driver.NewDocker(getter)
		mock := &mockDriver{}
		d.RegisterDriver(mock)
		time.Sleep(10 * time.Millisecond)
		d.Close()
	}
}

func BenchmarkJSONDecoding(b *testing.B) {
	containerJSON := []byte(`{
		"Id": "container1",
		"Config": {
			"Labels": {
				"baker.enable": "true",
				"baker.network": "baker_net",
				"baker.service.port": "8080",
				"baker.service.ping": "/health",
				"baker.service.static.domain": "example.com",
				"baker.service.static.path": "/*",
				"baker.service.static.headers.host": "example.com"
			}
		},
		"NetworkSettings": {
			"Networks": {
				"baker_net": {"IPAddress": "172.18.0.2"}
			}
		}
	}`)

	type containerPayload struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
		ID string `json:"Id"`
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var payload containerPayload
		_ = json.Unmarshal(containerJSON, &payload)
	}
}
