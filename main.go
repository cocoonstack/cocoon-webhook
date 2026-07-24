// Package main is the cocoon-webhook entry point. The webhook handles
// admission review for cocoon pods, workloads, and CocoonSet CRs.
package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	commonhttpx "github.com/cocoonstack/cocoon-common/httpx"
	commonk8s "github.com/cocoonstack/cocoon-common/k8s"
	commonlog "github.com/cocoonstack/cocoon-common/log"
	"github.com/cocoonstack/cocoon-webhook/admission"
	"github.com/cocoonstack/cocoon-webhook/certs"
	"github.com/cocoonstack/cocoon-webhook/metrics"
	"github.com/cocoonstack/cocoon-webhook/version"
)

const (
	defaultCertFile      = "/etc/cocoon/webhook/certs/tls.crt"
	defaultKeyFile       = "/etc/cocoon/webhook/certs/tls.key"
	defaultListen        = ":8443"
	defaultMetricsListen = ":9090"
	defaultPodCreators   = "system:serviceaccount:cocoon-system:cocoon-operator"

	shutdownTimeout = 15 * time.Second
)

func main() {
	ctx := context.Background()
	logger := log.WithFunc("main")
	if err := commonlog.Setup(ctx, "WEBHOOK_LOG_LEVEL"); err != nil {
		logger.Fatalf(ctx, err, "setup log")
	}

	prometheus.MustRegister(metrics.AdmissionTotal)

	certFile := commonk8s.EnvOrDefault("TLS_CERT", defaultCertFile)
	keyFile := commonk8s.EnvOrDefault("TLS_KEY", defaultKeyFile)
	listen := commonk8s.EnvOrDefault("LISTEN_ADDR", defaultListen)
	metricsListen := commonk8s.EnvOrDefault("METRICS_ADDR", defaultMetricsListen)

	reloader, err := certs.NewReloader(ctx, certFile, keyFile)
	if err != nil {
		logger.Fatalf(ctx, err, "load TLS keypair")
	}

	clientset, dyn, err := commonk8s.NewClientsetAndDynamic()
	if err != nil {
		logger.Fatalf(ctx, err, "build clientset")
	}

	podCreators := strings.Split(commonk8s.EnvOrDefault("POD_CREATORS", defaultPodCreators), ",")
	for i, c := range podCreators {
		podCreators[i] = strings.TrimSpace(c)
	}

	webhookServer := commonhttpx.NewServer(listen, admission.NewServer(clientset, dyn, podCreators).Routes())
	webhookServer.TLSConfig = &tls.Config{
		GetCertificate: reloader.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := commonhttpx.NewServer(metricsListen, metricsMux)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Infof(ctx, "cocoon-webhook %s started (rev=%s built=%s) on %s (metrics on %s)",
		version.VERSION, version.REVISION, version.BUILTAT, listen, metricsListen)

	specs := []commonhttpx.ServerSpec{
		commonhttpx.HTTPSServerSpec(webhookServer),
		commonhttpx.HTTPServerSpec(metricsServer),
	}
	if err := commonhttpx.Run(ctx, shutdownTimeout, specs...); err != nil {
		logger.Fatalf(ctx, err, "run servers")
	}
}
