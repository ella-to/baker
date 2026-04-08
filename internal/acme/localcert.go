package acme

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type localhostCertManager struct {
	cacheDir string

	caCert *x509.Certificate
	caKey  *rsa.PrivateKey

	mu    sync.Mutex
	certs map[string]*tls.Certificate
}

func newLocalhostCertManager(cachePath string) (*localhostCertManager, error) {
	cacheDir := filepath.Join(cachePath, "localhost")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create localhost cache directory: %w", err)
	}

	caCert, caKey, err := ensureCA(cacheDir)
	if err != nil {
		return nil, err
	}

	return &localhostCertManager{
		cacheDir: cacheDir,
		caCert:   caCert,
		caKey:    caKey,
		certs:    make(map[string]*tls.Certificate),
	}, nil
}

func (m *localhostCertManager) GetCertificate(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := normalizeHost(chi.ServerName)
	if !isLocalhostHost(host) {
		return nil, fmt.Errorf("local certificate requested for non-localhost host: %s", host)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if cert, ok := m.certs[host]; ok {
		return cert, nil
	}

	certPath, keyPath := m.leafPaths(host)
	cert, err := loadKeyPair(certPath, keyPath)
	if err == nil {
		m.certs[host] = cert
		return cert, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	cert, err = m.issueLeaf(host, certPath, keyPath)
	if err != nil {
		return nil, err
	}

	m.certs[host] = cert
	return cert, nil
}

func (m *localhostCertManager) issueLeaf(host, certPath, keyPath string) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key for %s: %w", host, err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: randomSerialNumber(),
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host},
	}

	if host == "localhost" {
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}

	derCert, err := x509.CreateCertificate(rand.Reader, template, m.caCert, &key.PublicKey, m.caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate for %s: %w", host, err)
	}

	if err := writeCertificatePEM(certPath, derCert); err != nil {
		return nil, err
	}

	if err := writeRSAPrivateKeyPEM(keyPath, key); err != nil {
		return nil, err
	}

	return loadKeyPair(certPath, keyPath)
}

func (m *localhostCertManager) leafPaths(host string) (string, string) {
	encodedHost := base64.RawURLEncoding.EncodeToString([]byte(host))
	certPath := filepath.Join(m.cacheDir, encodedHost+".crt")
	keyPath := filepath.Join(m.cacheDir, encodedHost+".key")
	return certPath, keyPath
}

func ensureCA(cacheDir string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPath := filepath.Join(cacheDir, "ca.crt")
	keyPath := filepath.Join(cacheDir, "ca.key")

	caCert, caKey, err := loadCA(certPath, keyPath)
	if err == nil {
		return caCert, caKey, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}

	caKey, err = rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate localhost CA private key: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: randomSerialNumber(),
		Subject: pkix.Name{
			CommonName:   "baker localhost CA",
			Organization: []string{"baker"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	derCert, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create localhost CA certificate: %w", err)
	}

	if err := writeCertificatePEM(certPath, derCert); err != nil {
		return nil, nil, err
	}

	if err := writeRSAPrivateKeyPEM(keyPath, caKey); err != nil {
		return nil, nil, err
	}

	caCert, err = parseCertificate(derCert)
	if err != nil {
		return nil, nil, err
	}

	return caCert, caKey, nil
}

func loadCA(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("failed to parse localhost CA cert pem")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse localhost CA certificate: %w", err)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" {
		return nil, nil, fmt.Errorf("failed to parse localhost CA key pem")
	}

	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse localhost CA private key: %w", err)
	}

	return cert, key, nil
}

func loadKeyPair(certPath, keyPath string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	return &cert, nil
}

func writeCertificatePEM(path string, derCert []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open certificate file %s: %w", path, err)
	}
	defer file.Close()

	if err := pem.Encode(file, &pem.Block{Type: "CERTIFICATE", Bytes: derCert}); err != nil {
		return fmt.Errorf("failed to write certificate file %s: %w", path, err)
	}

	return nil
}

func writeRSAPrivateKeyPEM(path string, key *rsa.PrivateKey) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open key file %s: %w", path, err)
	}
	defer file.Close()

	if err := pem.Encode(file, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		return fmt.Errorf("failed to write key file %s: %w", path, err)
	}

	return nil
}

func parseCertificate(derCert []byte) (*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(derCert)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}
	return cert, nil
}

func randomSerialNumber() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}

	return n
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")

	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}

	parsedHost, _, err := net.SplitHostPort(host)
	if err == nil {
		host = parsedHost
	}

	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}

	return host
}

func isLocalhostHost(host string) bool {
	host = normalizeHost(host)
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func LocalhostCAPath(cachePath string) string {
	if cachePath == "" {
		cachePath = "."
	}

	return filepath.Join(cachePath, "localhost", "ca.crt")
}
