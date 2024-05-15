package httpclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type Getter interface {
	Get(ctx context.Context, url string) (io.ReadCloser, int, error)
}

type GetterFunc func(ctx context.Context, url string) (io.ReadCloser, int, error)

var _ Getter = GetterFunc(nil)

func (fn GetterFunc) Get(ctx context.Context, url string) (io.ReadCloser, int, error) {
	return fn(ctx, url)
}

type client struct {
	host       string
	httpClient *http.Client
}

func (c *client) Get(ctx context.Context, url string) (io.ReadCloser, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%s", c.host, url), nil)
	if err != nil {
		return nil, 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}

	return resp.Body, resp.StatusCode, nil
}

type clientOption interface {
	configureClient(*client) error
}

type clientOptionFunc func(*client) error

var _ clientOption = clientOptionFunc(nil)

func (f clientOptionFunc) configureClient(c *client) error {
	return f(c)
}

func WithUnixSock(unixSockPath string, host string) clientOptionFunc {
	return func(c *client) error {
		if c.httpClient != nil {
			return fmt.Errorf("client already configured")
		}
		c.host = host
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
					return net.Dial("unix", unixSockPath)
				},
			},
		}

		return nil
	}
}

func WithHttpClient(httpClient *http.Client, host string) clientOptionFunc {
	return func(c *client) error {
		if c.httpClient != nil {
			return fmt.Errorf("client already configured")
		}

		c.host = host
		c.httpClient = httpClient
		return nil
	}
}

func WithHttpClientTimeout(timeout time.Duration, host string) clientOptionFunc {
	return func(c *client) error {
		if c.httpClient != nil {
			return fmt.Errorf("client not configured")
		}

		c.host = host
		c.httpClient = &http.Client{
			Timeout: timeout,
		}
		return nil
	}
}

func NewClient(opts ...clientOption) (Getter, error) {
	c := &client{}
	for _, opt := range opts {
		if err := opt.configureClient(c); err != nil {
			return nil, err
		}
	}

	if c.httpClient == nil {
		c.httpClient = &http.Client{}
	}

	return c, nil
}
