// Package certs provides a TLS keypair Reloader that re-reads the
// cert+key from disk when their mtime advances, so cert-manager
// rotations land without a pod restart.
package certs

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/projecteru2/core/log"
)

// Reloader caches a TLS keypair and re-reads it when either file's mtime
// advances past the cached load time. Reload errors fall through to the stale
// cert rather than drop in-flight handshakes.
//
// ctx is stashed for logging only: tls.Config.GetCertificate takes no ctx.
type Reloader struct {
	ctx      context.Context
	certFile string
	keyFile  string

	mu       sync.RWMutex
	cert     *tls.Certificate
	loadedAt time.Time
}

// NewReloader loads the initial keypair and returns a Reloader. Errors
// here are fatal — a webhook with no cert can't serve HTTPS.
func NewReloader(ctx context.Context, certFile, keyFile string) (*Reloader, error) {
	r := &Reloader{ctx: ctx, certFile: certFile, keyFile: keyFile}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate is the tls.Config.GetCertificate callback. It stats both files
// per handshake — cheap at admission rates. Concurrent handshakes during a
// rotation may each reload; tolerated to keep readers on RLock only, since
// every reader loads identical cert content.
func (r *Reloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if r.mtimeChanged() {
		if err := r.load(); err != nil {
			log.WithFunc("GetCertificate").Error(r.ctx, err, "reload TLS keypair, serving stale cert")
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

func (r *Reloader) load() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("load keypair %s/%s: %w", r.certFile, r.keyFile, err)
	}
	r.mu.Lock()
	r.cert = &cert
	r.loadedAt = time.Now()
	r.mu.Unlock()
	return nil
}

func (r *Reloader) mtimeChanged() bool {
	r.mu.RLock()
	loadedAt := r.loadedAt
	r.mu.RUnlock()
	for _, p := range []string{r.certFile, r.keyFile} {
		info, err := os.Stat(p)
		if err == nil && info.ModTime().After(loadedAt) {
			return true
		}
	}
	return false
}
