package baker_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"ella.to/baker"
)

var count int

func createDummyContainer(t *testing.T, config *baker.Config) *baker.Container {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("request", "host", r.Host, "path", r.URL.Path)

		if r.URL.Path == "/config" {

			b, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
			w.Write(b)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello world"))
	}))

	t.Cleanup(server.Close)

	count++

	addr, err := netip.ParseAddrPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	return &baker.Container{
		Id:         fmt.Sprintf("container-%d", count),
		ConfigPath: "/config",
		Addr:       addr,
	}
}

func createBakerServer(t *testing.T) (*baker.Server, string) {
	handler := baker.NewServer(baker.WithPingDuration(2 * time.Second))
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		handler.Close()
		server.Close()
	})

	return handler, server.URL
}

func TestServer(t *testing.T) {
	slog.SetLogLoggerLevel(slog.LevelDebug)

	container1 := createDummyContainer(t, &baker.Config{
		Endpoints: []baker.Endpoint{
			{Domain: "example.com", Path: "/ella/a"},
		},
	})

	server, url := createBakerServer(t)

	var driver baker.Driver

	server.RegisterDriver(func(d baker.Driver) {
		driver = d
	})

	driver.Add(container1)

	// Wait for the server to process the container
	time.Sleep(4 * time.Second)

	req, err := http.NewRequest(http.MethodGet, url+"/ella/a", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Host = "example.com"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status code 200, got %d", resp.StatusCode)
	}

	resp.Body.Close()
}
