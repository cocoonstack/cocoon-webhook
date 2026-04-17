# cocoon-webhook

Kubernetes admission webhook for the [cocoonstack](https://github.com/cocoonstack) VM platform.

## Overview

cocoon-webhook hosts three admission endpoints:

| Endpoint | Type | Resources | What it does |
|---|---|---|---|
| `POST /mutate` | Mutating | Pod CREATE | Rejects cocoon-tolerated pods that are not owned by a CocoonSet. CocoonSet-owned pods pass through unmutated. |
| `POST /validate` | Validating | Deployment / StatefulSet UPDATE | Rejects scale-down on cocoon-tolerated workloads (agents are stateful VMs — use `CocoonHibernation` to suspend instead). |
| `POST /validate-cocoonset` | Validating | CocoonSet CREATE / UPDATE | Catches the cross-field business rules the CRD's OpenAPI schema cannot express (image required, toolbox name uniqueness, static-mode prerequisites). |
| `GET /healthz` | Liveness | — | Always 200 once the binary is running. |
| `GET /readyz` | Readiness | — | 200 once dependencies needed to serve admission traffic are reachable. |
| `GET /metrics` | Prometheus | — | Plain HTTP on `:9090`, separate from the admission TLS port. |

## CocoonSet validation rules

The CRD ships with `+kubebuilder` enum / required / default markers, but the webhook adds the cross-field business rules:

- `spec.agent.image` must be set
- `spec.agent.replicas >= 0`
- `spec.agent.mode ∈ {clone, run}`
- `spec.agent.os ∈ {linux, windows, android}`
- `spec.agent.backend ∈ {cloud-hypervisor, firecracker}`
- `spec.agent.connType ∈ {ssh, rdp, vnc, adb}`
- firecracker + `os=windows` is rejected (FC cannot boot Windows guests)
- firecracker + cloudimg URL image is rejected (FC requires OCI images)
- firecracker + `mode=clone` is rejected (FC snapshot/restore freezes guest network state; use `mode=run`)
- `spec.toolboxes[*].name` unique and matches RFC 1123
- `spec.toolboxes[*]` static mode requires both `staticIP` and `staticVMID`
- `spec.toolboxes[*]` non-static modes require `image`
- `spec.toolboxes[*].backend` must match `spec.agent.backend` (static toolboxes skip this check)
- `spec.snapshotPolicy ∈ {always, main-only, never}`

## Configuration

| Variable | Default | Description |
|---|---|---|
| `KUBECONFIG` | unset | Path to kubeconfig when running outside the cluster (in-cluster config used otherwise) |
| `WEBHOOK_LOG_LEVEL` | `info` | `projecteru2/core/log` level |
| `TLS_CERT` | `/etc/cocoon/webhook/certs/tls.crt` | TLS server certificate |
| `TLS_KEY` | `/etc/cocoon/webhook/certs/tls.key` | TLS server private key |
| `LISTEN_ADDR` | `:8443` | Admission listener (HTTPS) |
| `METRICS_ADDR` | `:9090` | Prometheus listener (HTTP) |

## Installation

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

To override the image tag or replica count, build a kustomize overlay that imports `config/default` as a base.

## Development

```bash
make all            # full pipeline: deps + fmt + lint + test + build
make build          # build cocoon-webhook binary
make test           # vet + race-detected tests
make lint           # golangci-lint on linux + darwin
make fmt            # gofumpt + goimports
make help           # show all targets
```

The Makefile detects Go workspace mode (`go env GOWORK`) and skips `go mod tidy` when active so cross-module references resolve through `go.work` without forcing a release of cocoon-common.

## Related projects

| Project | Role |
|---|---|
| [cocoon-common](https://github.com/cocoonstack/cocoon-common) | CRD types, annotation contract, shared helpers |
| [cocoon-operator](https://github.com/cocoonstack/cocoon-operator) | CocoonSet and CocoonHibernation reconcilers |
| [epoch](https://github.com/cocoonstack/epoch) | Snapshot registry and storage backend |
| [vk-cocoon](https://github.com/cocoonstack/vk-cocoon) | Virtual kubelet provider managing VM lifecycle |

## License

[MIT](LICENSE)
