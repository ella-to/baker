package acme

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// HostPolicy is a function that decides whether the given host is allowed.
// It returns nil if the host is allowed, or an error if not.
type HostPolicy func(ctx context.Context, host string) error

// ErrHostNotAllowed is returned when a host is not in the allowed domains list.
var ErrHostNotAllowed = errors.New("acme: host not allowed")

func Start(handler http.Handler, cachePath string, hostPolicy HostPolicy) error {
	if cachePath == "" {
		cachePath = "."
	}

	localhostManager, err := newLocalhostCertManager(cachePath)
	if err != nil {
		return err
	}

	certManager := autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(cachePath),
		HostPolicy: func(ctx context.Context, host string) error {
			host = normalizeHost(host)
			if hostPolicy == nil {
				return nil
			}

			return hostPolicy(ctx, host)
		},
	}

	httpsServer := &http.Server{
		Addr:         ":443",
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
				host := normalizeHost(chi.ServerName)
				if isLocalhostHost(host) {
					if hostPolicy != nil {
						if err := hostPolicy(context.Background(), host); err != nil {
							return nil, err
						}
					}

					return localhostManager.GetCertificate(chi)
				}

				return certManager.GetCertificate(chi)
			},
		},
	}

	httpServer := &http.Server{
		Addr:         ":80",
		Handler:      certManager.HTTPHandler(nil),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	httpClose := make(chan struct{}, 1)
	httpsClose := make(chan struct{}, 1)
	errs := make(chan error, 2)

	go func() {
		defer close(httpClose)
		err := httpServer.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	go func() {
		defer close(httpsClose)
		err := httpsServer.ListenAndServeTLS("", "")
		if !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case <-httpClose:
		_ = httpsServer.Shutdown(context.Background())
	case <-httpsClose:
		_ = httpServer.Shutdown(context.Background())
	}

	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}
