// cocoon-webhook — Admission webhook for stateful VM scheduling and protection.
//
// Mutating (/mutate — Pod CREATE):
//  1. Derives a stable VM name from the Deployment/ReplicaSet owner + replica slot
//  2. Looks up the ConfigMap "cocoon-vm-affinity" for last-known node
//  3. Sets pod.spec.nodeName + cocoon.cis/vm-name annotation
//
// Validating (/validate — Deployment/StatefulSet UPDATE):
//  4. Blocks scale-down for cocoon-type workloads (only scale-up allowed)
//     Agents are stateful VMs — reducing replicas would destroy state.
//     Use the Hibernation CRD to suspend individual agents instead.
//
// For pods without a Deployment owner (bare pods, StatefulSets), the
// webhook uses the pod name directly as the VM name.
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
	"k8s.io/client-go/kubernetes"

	commonk8s "github.com/cocoonstack/cocoon-common/k8s"
	commonlog "github.com/cocoonstack/cocoon-common/log"
	"github.com/cocoonstack/cocoon-webhook/version"
)

func main() {
	ctx := context.Background()
	commonlog.Setup(ctx, "WEBHOOK_LOG_LEVEL")

	logger := log.WithFunc("main")

	config, err := commonk8s.LoadConfig()
	if err != nil {
		logger.Fatalf(ctx, err, "load k8s config")
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Fatalf(ctx, err, "init clientset")
	}

	certFile := envOrDefault("TLS_CERT", "/etc/cocoon/webhook/certs/tls.crt")
	keyFile := envOrDefault("TLS_KEY", "/etc/cocoon/webhook/certs/tls.key")

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		logger.Fatalf(ctx, err, "load TLS keypair")
	}

	server := &http.Server{
		Addr:              ":8443",
		Handler:           newWebhookServer(clientset).routes(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		logger.Infof(ctx, "cocoon-webhook %s started (rev=%s built=%s) on :8443", version.VERSION, version.REVISION, version.BUILTAT)
		if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Fatalf(ctx, serveErr, "listen and serve")
		}
	}()

	<-ctx.Done()
	shutdownCtx := context.Background()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warnf(shutdownCtx, "shutdown: %v", err)
	}
}
