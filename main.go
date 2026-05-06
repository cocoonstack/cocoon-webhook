// Package main is the cocoon-webhook entry point. The webhook handles
// admission review for cocoon pods, workloads, and CocoonSet CRs, and
// owns the per-pool node-affinity reservation store.
package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/prometheus/client_golang/prometheus"

	commonhttpx "github.com/cocoonstack/cocoon-common/httpx"
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

	shutdownTimeout = 15 * time.Second
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

	clientset, err := commonk8s.NewClientset()
	if err != nil {
		logger.Fatalf(ctx, err, "build clientset: %v", err)
	}

	webhookServer := commonhttpx.NewServer(listen, admission.NewServer(clientset).Routes())
	webhookServer.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Handler())
	metricsServer := commonhttpx.NewServer(metricsListen, metricsMux)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Infof(ctx, "cocoon-webhook %s started (rev=%s built=%s) on %s (metrics on %s)",
		version.VERSION, version.REVISION, version.BUILTAT, listen, metricsListen)

	specs := []commonhttpx.ServerSpec{
		commonhttpx.HTTPSServerSpec(webhookServer, "", ""),
		commonhttpx.HTTPServerSpec(metricsServer),
	}
	if err := commonhttpx.Run(ctx, shutdownTimeout, specs...); err != nil {
		logger.Error(ctx, err, "run servers")
	}
}
