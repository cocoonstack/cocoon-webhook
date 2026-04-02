# cocoon-webhook

Kubernetes admission webhook for sticky scheduling of VM-backed pods in [cocoonstack](https://github.com/cocoonstack) clusters.

## Overview

- **Mutating** (`POST /mutate`) -- on Pod CREATE, derives a stable VM name from the Deployment or ReplicaSet owner chain, looks up the previously assigned node in the `cocoon-vm-affinity` ConfigMap, and patches `spec.nodeName` so the pod returns to the same worker
- **Validating** (`POST /validate`) -- on Deployment or StatefulSet UPDATE, blocks scale-down for cocoon-type workloads and preserves VM state
- **Health check** -- served on `GET /healthz`

Recommended for multi-worker cocoon pools where restart affinity matters and Deployments recreate VM-backed pods while expecting state continuity.

## Architecture

The webhook exposes three handlers:

- `POST /mutate` for sticky node placement and stable VM naming
- `POST /validate` for scale-down protection
- `GET /healthz` for readiness checks

It uses the Kubernetes API to read the `cocoon-vm-affinity` ConfigMap and current node inventory, then returns standard admission patches or validation rejections.

## Installation

### Download

Download a pre-built binary from [GitHub Releases](https://github.com/cocoonstack/cocoon-webhook/releases):

```bash
# Linux (amd64)
curl -fSL -o cocoon-webhook \
  "https://github.com/cocoonstack/cocoon-webhook/releases/latest/download/cocoon-webhook-linux-amd64"
chmod +x cocoon-webhook

# macOS (amd64)
curl -fSL -o cocoon-webhook \
  "https://github.com/cocoonstack/cocoon-webhook/releases/latest/download/cocoon-webhook-darwin-amd64"
chmod +x cocoon-webhook
```

### Build from source

```bash
git clone https://github.com/cocoonstack/cocoon-webhook.git
cd cocoon-webhook
make build          # produces ./cocoon-webhook
```

## Configuration

The binary expects TLS certificates and listens on `:8443`.

| Variable | Default | Description |
|---|---|---|
| `KUBECONFIG` | `~/.kube/config` | Path to kubeconfig when running outside the cluster |
| `WEBHOOK_LOG_LEVEL` | `info` | Log level for the webhook process |
| `TLS_CERT` | `/etc/cocoon/webhook/certs/tls.crt` | Path to TLS certificate |
| `TLS_KEY` | `/etc/cocoon/webhook/certs/tls.key` | Path to TLS private key |

## Quick Start

```bash
export TLS_CERT=/etc/cocoon/webhook/certs/tls.crt
export TLS_KEY=/etc/cocoon/webhook/certs/tls.key

./cocoon-webhook
```

Package it behind a standard Kubernetes Deployment, Service, and webhook configuration, or run it on a control-plane host if that fits your environment.

## Development

```bash
make build          # build binary
make test           # vet and race-detected tests with coverage
make lint           # run golangci-lint
make fmt            # format code
make help           # show all targets
```

## Related Projects

| Project | Role |
|---|---|
| [cocoon-common](https://github.com/cocoonstack/cocoon-common) | Shared metadata, Kubernetes, and logging helpers |
| [cocoon-operator](https://github.com/cocoonstack/cocoon-operator) | CocoonSet and Hibernation CRDs |
| [epoch](https://github.com/cocoonstack/epoch) | Remote snapshot storage |
| [vk-cocoon](https://github.com/cocoonstack/vk-cocoon) | Virtual kubelet provider managing VM lifecycle |

## License

[MIT](LICENSE)
