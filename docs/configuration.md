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
| `POD_CREATORS` | `system:serviceaccount:cocoon-system:cocoon-operator` | Comma-separated requester usernames allowed to create Pods inside the cocoon gate or move Pods into it on UPDATE |

The cocoon gate covers Pods with the `virtual-kubelet.io/provider` toleration
or a non-empty `vm.cocoonstack.io/name` annotation. Entry requires both a
CocoonSet owner and an allowlisted requester. UPDATEs to already gated Pods
skip this entry check so controller runtime patches remain allowed.

The admission server caps each request body at 10 MiB.
