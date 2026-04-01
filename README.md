# cocoon-webhook

Kubernetes admission webhook for sticky scheduling of VM-backed pods in [cocoonstack](https://github.com/cocoonstack) clusters.

## Overview

cocoon-webhook provides two admission endpoints:

- **Mutating** (`POST /mutate`) -- on Pod CREATE, derives a stable VM name from the Deployment/ReplicaSet owner chain, looks up the previously assigned node in the `cocoon-vm-affinity` ConfigMap, and patches `spec.nodeName` so the pod returns to the same worker. Writes the `cocoon.cis/vm-name` annotation when missing.

- **Validating** (`POST /validate`) -- on Deployment/StatefulSet UPDATE, blocks scale-down for cocoon-type workloads. Agents are stateful VMs; reducing replicas would destroy state. Use the Hibernation CRD to suspend individual agents instead.

A health check is served on `GET /healthz`.

## When to use

Recommended for:

- Multi-worker cocoon pools where restart affinity matters
- Deployments that recreate VM-backed pods while expecting state continuity

Often unnecessary for:

- Single-worker labs
- Setups that pin workloads explicitly with `nodeName`
- Setups that rely only on CocoonSet (the controller handles placement)

## Installation

Download a pre-built binary from [GitHub Releases](https://github.com/cocoonstack/cocoon-webhook/releases):

```bash
# Download the latest release (Linux amd64)
curl -fSL -o cocoon-webhook \
  "https://github.com/cocoonstack/cocoon-webhook/releases/latest/download/cocoon-webhook-linux-amd64"
chmod +x cocoon-webhook

# Or for macOS (amd64)
curl -fSL -o cocoon-webhook \
  "https://github.com/cocoonstack/cocoon-webhook/releases/latest/download/cocoon-webhook-darwin-amd64"
chmod +x cocoon-webhook
```

Alternatively, build from source (see below).

## Building

```bash
make build        # produces ./cocoon-webhook
make test         # vet + race-detected tests with coverage
make lint         # golangci-lint for linux and darwin
```

See the [Makefile](Makefile) for the full list of targets (`make help`).

## Deployment

The binary expects TLS certificates (configurable via `TLS_CERT` / `TLS_KEY` environment variables, defaulting to `/etc/webhook/certs/tls.crt` and `tls.key`). It listens on `:8443`.

Package it behind a standard Kubernetes Deployment, Service, and MutatingWebhookConfiguration, or run it on a control-plane host if that fits your environment.

## Related projects

| Project | Role |
|---------|------|
| [cocoon](https://github.com/cocoonstack/cocoon) | Virtual-kubelet provider managing VM lifecycle |
| cocoon-operator | CocoonSet and Hibernation CRDs |
| epoch | Remote snapshot storage |
| glance | Web dashboard (does not depend on this webhook) |

## License

[MIT](LICENSE)
