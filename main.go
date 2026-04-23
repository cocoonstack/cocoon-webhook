package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes"

	commonk8s "github.com/cocoonstack/cocoon-common/k8s"
	commonlog "github.com/cocoonstack/cocoon-common/log"
	"github.com/cocoonstack/cocoon-webhook/admission"
	"github.com/cocoonstack/cocoon-webhook/metrics"
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

	metrics.Register(prometheus.DefaultRegisterer)

	certFile := commonk8s.EnvOrDefault("TLS_CERT", defaultCertFile)
	keyFile := commonk8s.EnvOrDefault("TLS_KEY", defaultKeyFile)
	listen := commonk8s.EnvOrDefault("LISTEN_ADDR", defaultListen)
	metricsListen := commonk8s.EnvOrDefault("METRICS_ADDR", defaultMetricsListen)

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

	server := &http.Server{
		Addr:              listen,
		Handler:           admission.NewServer(clientset).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	// Capture root ctx before signal.NotifyContext shadows it; shutdown must
	// outlive the signal-derived ctx (which is canceled the moment we handle SIGINT/SIGTERM).
	rootCtx := ctx
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Handler())
	metricsServer := &http.Server{
		Addr:              metricsListen,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.Infof(ctx, "cocoon-webhook metrics listening on %s", metricsListen)
		if serveErr := metricsServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error(ctx, serveErr, "metrics listen and serve")
			cancel()
		}
	}()

	go func() {
		logger.Infof(ctx, "cocoon-webhook %s started (rev=%s built=%s) on %s",
			version.VERSION, version.REVISION, version.BUILTAT, listen)
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error(ctx, serveErr, "listen and serve")
			cancel()
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(rootCtx, 15*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warnf(shutdownCtx, "shutdown admission: %v", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Warnf(shutdownCtx, "shutdown metrics: %v", err)
	}
}
