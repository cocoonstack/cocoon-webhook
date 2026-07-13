# Installation

The supported install path is `kubectl apply -k`:

```bash
kubectl apply -k github.com/cocoonstack/cocoon-webhook/config/default?ref=main
```

This installs:
- `cocoon-system` namespace
- `ServiceAccount` + `ClusterRole` (read deployments/statefulsets for scale-down validation)
- cert-manager `Issuer` + `Certificate` (`cocoon-webhook-tls`) — **cert-manager must already be installed in the cluster**
- `Deployment` (2 replicas) + `Service` (port 443 → 8443, port 9090 → 9090)
- `MutatingWebhookConfiguration` for Pod CREATE
- `ValidatingWebhookConfiguration` for Deployment/StatefulSet UPDATE and CocoonSet CREATE/UPDATE

To override the image tag or replica count, build a kustomize overlay
that imports `config/default` as a base. See
[Configuration](configuration.md) for the environment variables the
resulting Deployment can set.

## Building from source

```bash
make all            # full pipeline: deps + fmt + lint + test + build
make build          # build cocoon-webhook binary
make test           # vet + race-detected tests
make lint           # golangci-lint on linux + darwin
make fmt            # gofumpt + goimports
make help           # show all targets
```

The Makefile detects Go workspace mode (`go env GOWORK`) and skips `go
mod tidy` when active so cross-module references resolve through
`go.work` without forcing a release of cocoon-common.
