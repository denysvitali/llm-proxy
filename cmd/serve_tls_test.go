package cmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestServeListenerRequiresTrustedCertificate(t *testing.T) {
	certPEM, keyPEM := listenerCertificate(t)
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	serverConfig := config.ServerConfig{TLSCertFile: certFile, TLSKeyFile: keyFile}
	listener := serveTestListener(t, serverConfig)

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("test certificate did not parse")
	}
	trusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    pool,
		ServerName: "llm-proxy.test",
		MinVersion: tls.VersionTLS12,
	}}}
	response, err := trusted.Get("https://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatalf("trusted client: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.TLS == nil || response.TLS.Version < tls.VersionTLS12 {
		t.Fatalf("status %d, TLS %#v", response.StatusCode, response.TLS)
	}

	untrusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		ServerName: "llm-proxy.test",
		MinVersion: tls.VersionTLS12,
	}}}
	if _, err := untrusted.Get("https://" + listener.Addr().String() + "/"); err == nil {
		t.Fatal("client without the listener certificate completed the handshake")
	}
}

func TestServeListenerStaysPlaintextWithoutCertificate(t *testing.T) {
	listener := serveTestListener(t, config.ServerConfig{})
	response, err := http.Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.TLS != nil {
		t.Fatalf("plaintext listener status %d, TLS %#v", response.StatusCode, response.TLS)
	}
	if _, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true}); err == nil {
		t.Fatal("plaintext listener accepted a TLS handshake")
	}
}

// serveTestListener binds an ephemeral loopback port and serves it with the
// same transport choice as the process.
func serveTestListener(t *testing.T, serverConfig config.ServerConfig) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		ReadHeaderTimeout: time.Second,
		TLSConfig:         listenerTLSConfig(serverConfig),
	}
	errCh := make(chan error, 1)
	go func() { errCh <- serveListener(httpServer, listener, serverConfig) }()
	t.Cleanup(func() {
		_ = httpServer.Close()
		if err := <-errCh; err != nil && err != http.ErrServerClosed {
			t.Errorf("listener: %v", err)
		}
	})
	return listener
}

func listenerCertificate(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "llm-proxy.test"},
		DNSNames:     []string{"llm-proxy.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
