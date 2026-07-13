# Overview

cocoon-webhook is a Kubernetes admission webhook that enforces cocoon
sticky scheduling and validates [CocoonSet](https://github.com/cocoonstack/cocoon-operator)
resources beyond what the CRD's OpenAPI schema can express. It hosts
three admission endpoints plus health and metrics surfaces:

| Endpoint | Type | Resources | What it does |
|---|---|---|---|
| `POST /mutate` | Mutating | Pod CREATE | Rejects cocoon-tolerated pods that are not owned by a CocoonSet. CocoonSet-owned pods pass through unmutated. |
| `POST /validate` | Validating | Deployment / StatefulSet UPDATE | Rejects scale-down on cocoon-tolerated workloads. Bypass path for hand-rolled Deployments/StatefulSets carrying the cocoon toleration — the CocoonSet main flow creates Pods directly and does not traverse this endpoint. |
| `POST /validate-cocoonset` | Validating | CocoonSet CREATE / UPDATE | Catches the cross-field business rules the CRD's OpenAPI schema cannot express (image required, toolbox name uniqueness, static-mode prerequisites). See [CocoonSet validation rules](validation.md). |
| `GET /healthz` | Liveness | — | Always 200 once the binary is running. |
| `GET /readyz` | Readiness | — | Always 200 once the binary is running (liveness-equivalent stub; does not probe apiserver reachability). |
| `GET /metrics` | Prometheus | — | Plain HTTP on `:9090`, separate from the admission TLS port. |

The admission TLS listener reloads its certificate and key from disk
whenever their mtime advances, so a cert-manager rotation lands without
a pod restart.
