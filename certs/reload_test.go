package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeKeypair(t *testing.T, dir, cn string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	certPEM, _ := os.Create(certPath)
	defer certPEM.Close()
	if err := pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("encode cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshalkey: %v", err)
	}
	keyPEM, _ := os.Create(keyPath)
	defer keyPEM.Close()
	if err := pem.Encode(keyPEM, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatalf("encode key: %v", err)
	}
	return certPath, keyPath
}

func leafCN(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	return cert.Subject.CommonName
}

func TestReloaderInitialLoad(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeKeypair(t, dir, "first")

	r, err := NewReloader(context.Background(), certPath, keyPath)
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if got := leafCN(t, leaf); got != "first" {
		t.Errorf("CN = %q, want first", got)
	}
}

func TestReloaderPicksUpRotation(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeKeypair(t, dir, "first")

	r, err := NewReloader(context.Background(), certPath, keyPath)
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}

	// Overwrite with a fresh keypair and bump mtime ahead of the load.
	_, _ = writeKeypair(t, dir, "second")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatalf("chtimes cert: %v", err)
	}
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatalf("chtimes key: %v", err)
	}

	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after rotation: %v", err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if got := leafCN(t, leaf); got != "second" {
		t.Errorf("CN = %q, want second", got)
	}
}

func TestReloaderServesStaleOnReloadFailure(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeKeypair(t, dir, "first")

	r, err := NewReloader(context.Background(), certPath, keyPath)
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}

	// Truncate the cert to garbage, bump mtime so the reloader tries it.
	if err := os.WriteFile(certPath, []byte("not a pem"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatalf("chtimes cert: %v", err)
	}

	// Reload fails, but GetCertificate must still return the original cert.
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	if got := leafCN(t, leaf); got != "first" {
		t.Errorf("stale CN = %q, want first (reload should fall through)", got)
	}
}
