// Package certs provides a TLS keypair Reloader that re-reads the
// cert+key from disk when their mtime changes, so cert-manager
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
// differs from the load-time snapshot. Reload errors fall through to the
// stale cert rather than drop in-flight handshakes.
//
// ctx is stashed for logging only: tls.Config.GetCertificate takes no ctx.
type Reloader struct {
	ctx      context.Context
	certFile string
	keyFile  string

	mu           sync.RWMutex
	cert         *tls.Certificate
	loadedMTimes [2]time.Time
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

// GetCertificate is the tls.Config.GetCertificate callback. It stats both
// files per handshake — cheap at admission rates; concurrent handshakes may
// each reload during a rotation, tolerated to keep readers on RLock only.
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

// load stats before reading so a rotation racing the read is caught by the
// next mtime check instead of being masked by a post-read timestamp.
func (r *Reloader) load() error {
	mtimes := r.statMTimes()
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("load keypair %s/%s: %w", r.certFile, r.keyFile, err)
	}
	r.mu.Lock()
	r.cert = &cert
	r.loadedMTimes = mtimes
	r.mu.Unlock()
	return nil
}

func (r *Reloader) mtimeChanged() bool {
	r.mu.RLock()
	loaded := r.loadedMTimes
	r.mu.RUnlock()
	current := r.statMTimes()
	for i, mtime := range current {
		// Zero mtime means a stat error (e.g. mid-rotation swap): keep the
		// stale cert and let a later handshake retry.
		if !mtime.IsZero() && !mtime.Equal(loaded[i]) {
			return true
		}
	}
	return false
}

func (r *Reloader) statMTimes() [2]time.Time {
	var mtimes [2]time.Time
	for i, p := range []string{r.certFile, r.keyFile} {
		if info, err := os.Stat(p); err == nil {
			mtimes[i] = info.ModTime()
		}
	}
	return mtimes
}
