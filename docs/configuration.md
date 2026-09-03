# Configuration

cocoon-webhook is configured entirely through environment variables.

| Variable | Default | Description |
|---|---|---|
| `KUBECONFIG` | unset | Path list merged as `kubectl` does; falls back to `~/.kube/config`, then in-cluster config |
| `COCOON_K8S_QPS` | `50` | Kubernetes client QPS |
| `COCOON_K8S_BURST` | `100` | Kubernetes client burst |
| `WEBHOOK_LOG_LEVEL` | `info` | `projecteru2/core/log` level |
| `TLS_CERT` | `/etc/cocoon/webhook/certs/tls.crt` | TLS server certificate |
| `TLS_KEY` | `/etc/cocoon/webhook/certs/tls.key` | TLS server private key |
| `LISTEN_ADDR` | `:8443` | Admission listener (HTTPS) |
| `METRICS_ADDR` | `:9090` | Prometheus listener (HTTP) |
| `POD_CREATORS` | `system:serviceaccount:cocoon-system:cocoon-operator` | Comma-separated usernames allowed to create cocoon-tolerated pods |

The admission server caps each request body at 10 MiB.
