// cocoon-webhook is the cocoonstack admission webhook. It will host:
//
//   - a mutating endpoint that pins managed pods to a stable VM name
//     and a sticky cocoon node;
//   - validating endpoints that block destructive scale-down on cocoon
//     workloads and reject malformed CocoonSet specs.
//
// This file is the binary entry point. The handlers themselves live
// in the admission package; affinity bookkeeping in the affinity
// package; prometheus collectors in the metrics package.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/env"

	commonk8s "github.com/cocoonstack/cocoon-common/k8s"
	commonlog "github.com/cocoonstack/cocoon-common/log"
	"github.com/cocoonstack/cocoon-webhook/admission"
	"github.com/cocoonstack/cocoon-webhook/affinity"
	"github.com/cocoonstack/cocoon-webhook/metrics"
	"github.com/cocoonstack/cocoon-webhook/version"
)

const (
	defaultCertFile      = "/etc/cocoon/webhook/certs/tls.crt"
	defaultKeyFile       = "/etc/cocoon/webhook/certs/tls.key"
	defaultListen        = ":8443"
	defaultMetricsListen = ":9090"

	// informerResync is set to 0 because neither the picker nor the
	// reaper register UpdateFunc handlers — they only read the
	// lister. The watch stream is authoritative, and a periodic
	// resync would re-emit every cached object on every tick for no
	// downstream benefit.
	informerResync = 0
)

// envDuration parses a duration env var. Empty / unparseable falls
// back to the supplied default so the binary stays bootable when
// an operator typoes the override.
func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func main() {
	ctx := context.Background()
	commonlog.Setup(ctx, "WEBHOOK_LOG_LEVEL")

	logger := log.WithFunc("main")

	metrics.Register(prometheus.DefaultRegisterer)

	certFile := env.GetString("TLS_CERT", defaultCertFile)
	keyFile := env.GetString("TLS_KEY", defaultKeyFile)
	listen := env.GetString("LISTEN_ADDR", defaultListen)
	metricsListen := env.GetString("METRICS_ADDR", defaultMetricsListen)

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

	// Shared informer factory: pod and node informers feed both the
	// admission hot path (LeastUsedPicker) and the background reaper.
	// Calling Lister() registers each informer with the factory.
	informerFactory := informers.NewSharedInformerFactory(clientset, informerResync)
	podLister := informerFactory.Core().V1().Pods().Lister()
	nodeLister := informerFactory.Core().V1().Nodes().Lister()

	picker := affinity.NewLeastUsedPicker(podLister, nodeLister)
	affinityStore := affinity.NewConfigMapStore(clientset, picker)
	reaper := affinity.NewReaper(affinityStore, clientset, podLister)
	reaper.Interval = envDuration("REAPER_INTERVAL", reaper.Interval)
	reaper.Grace = envDuration("REAPER_GRACE", reaper.Grace)

	server := &http.Server{
		Addr:              listen,
		Handler:           admission.NewServer(clientset, affinityStore).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start the informers and block until their caches are warm.
	// Serving admission requests before sync would mean the picker
	// sees an empty pod set and concentrates every fresh pod onto
	// whichever node sorts first alphabetically.
	informerFactory.Start(ctx.Done())
	var unsynced []string
	for typ, ok := range informerFactory.WaitForCacheSync(ctx.Done()) {
		if !ok {
			unsynced = append(unsynced, typ.String())
		}
	}
	if len(unsynced) > 0 {
		syncErr := fmt.Errorf("informer cache sync failed for %v", unsynced)
		logger.Fatalf(ctx, syncErr, "informer cache sync: %v", syncErr)
	}
	logger.Info(ctx, "informer caches synced")

	go reaper.Run(ctx)

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
	// Shutdown gets a fresh ctx because the parent ctx is already
	// canceled by the signal handler. The 15s budget bounds how long
	// in-flight admission requests get to drain before the process
	// exits — long enough for healthy handlers, short enough that a
	// stuck connection cannot wedge the pod indefinitely.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warnf(shutdownCtx, "shutdown admission: %v", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Warnf(shutdownCtx, "shutdown metrics: %v", err)
	}
}
