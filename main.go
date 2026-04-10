// cocoon-webhook is the cocoonstack admission webhook. It will host:
//
//   - a mutating endpoint that pins managed pods to a stable VM name
//     and a sticky cocoon node;
//   - validating endpoints that block destructive scale-down on cocoon
//     workloads and reject malformed CocoonSet specs.
//
// This file is the binary entry point. The actual handlers are wired
// in by routes.go and live in their own per-feature files.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes"

	commonk8s "github.com/cocoonstack/cocoon-common/k8s"
	commonlog "github.com/cocoonstack/cocoon-common/log"
	"github.com/cocoonstack/cocoon-webhook/version"
)

const (
	defaultCertFile      = "/etc/cocoon/webhook/certs/tls.crt"
	defaultKeyFile       = "/etc/cocoon/webhook/certs/tls.key"
	defaultListen        = ":8443"
	defaultMetricsListen = ":9090"
)

func main() {
	ctx := context.Background()
	commonlog.Setup(ctx, "WEBHOOK_LOG_LEVEL")

	logger := log.WithFunc("main")

	RegisterMetrics(prometheus.DefaultRegisterer)

	certFile := envOrDefault("TLS_CERT", defaultCertFile)
	keyFile := envOrDefault("TLS_KEY", defaultKeyFile)
	listen := envOrDefault("LISTEN_ADDR", defaultListen)
	metricsListen := envOrDefault("METRICS_ADDR", defaultMetricsListen)

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		logger.Fatalf(ctx, err, "load TLS keypair: %v", err)
	}

	kubeConfig, err := commonk8s.LoadConfig()
	if err != nil {
		logger.Fatalf(ctx, err, "load kubeconfig: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		logger.Fatalf(ctx, err, "build clientset: %v", err)
	}

	picker := NewLeastUsedPicker(clientset)
	affinityStore := NewConfigMapStore(clientset, picker)
	reaper := NewReaper(affinityStore, clientset)

	server := &http.Server{
		Addr:              listen,
		Handler:           NewServer(clientset, affinityStore).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go reaper.Run(ctx)

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metricsHandler())
	metricsServer := &http.Server{
		Addr:              metricsListen,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.Infof(ctx, "cocoon-webhook metrics listening on %s", metricsListen)
		if serveErr := metricsServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Errorf(ctx, serveErr, "metrics listen and serve")
		}
	}()

	go func() {
		logger.Infof(ctx, "cocoon-webhook %s started (rev=%s built=%s) on %s",
			version.VERSION, version.REVISION, version.BUILTAT, listen)
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Fatalf(ctx, serveErr, "listen and serve: %v", serveErr)
		}
	}()

	<-ctx.Done()
	shutdownCtx := context.Background()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warnf(shutdownCtx, "shutdown admission: %v", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Warnf(shutdownCtx, "shutdown metrics: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
