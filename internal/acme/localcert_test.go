package acme

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		name string
		in   string
		out  string
	}{
		{name: "lowercase and trim", in: "  API.LOCALHOST.  ", out: "api.localhost"},
		{name: "strip port", in: "api.localhost:443", out: "api.localhost"},
		{name: "strip ipv6 brackets", in: "[::1]", out: "::1"},
		{name: "strip ipv6 brackets with port", in: "[::1]:443", out: "::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.out, normalizeHost(tt.in))
		})
	}
}

func TestIsLocalhostHost(t *testing.T) {
	require.True(t, isLocalhostHost("localhost"))
	require.True(t, isLocalhostHost("app.localhost"))
	require.True(t, isLocalhostHost("APP.LOCALHOST:443"))
	require.False(t, isLocalhostHost("example.com"))
	require.False(t, isLocalhostHost("notlocalhost"))
}

func TestLocalhostCertManagerGetCertificate(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := newLocalhostCertManager(tmpDir)
	require.NoError(t, err)

	cert, err := mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "demo.localhost"})
	require.NoError(t, err)
	require.NotNil(t, cert)
	require.NotEmpty(t, cert.Certificate)

	loadedCert, err := mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "demo.localhost"})
	require.NoError(t, err)
	require.NotNil(t, loadedCert)
	require.Equal(t, cert.Certificate[0], loadedCert.Certificate[0])
}
