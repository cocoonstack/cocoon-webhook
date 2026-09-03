# Overview

cocoon-webhook is a Kubernetes admission webhook that enforces cocoon
sticky scheduling and validates [CocoonSet](https://github.com/cocoonstack/cocoon-operator)
resources beyond what the CRD's OpenAPI schema can express. It hosts
four admission endpoints plus health and metrics surfaces:

| Endpoint | Type | Resources | What it does |
|---|---|---|---|
| `POST /mutate` | Mutating | Pod CREATE | Rejects cocoon-tolerated pods that are not owned by a CocoonSet or not created by an allowlisted controller identity (`POD_CREATORS`); owner references are client-settable, so the authenticated requester is what actually gates. Legitimate pods pass through unmutated. |
| `POST /validate` | Validating | Deployment / StatefulSet UPDATE | Rejects scale-down on cocoon-tolerated workloads. Bypass path for hand-rolled Deployments/StatefulSets carrying the cocoon toleration — the CocoonSet main flow creates Pods directly and does not traverse this endpoint. |
| `POST /validate-cocoonset` | Validating | CocoonSet CREATE / UPDATE | Catches the cross-field business rules the CRD's OpenAPI schema cannot express (image required, toolbox name uniqueness, static-mode prerequisites). See [Validation rules](validation.md). |
| `POST /validate-cocoonhibernation` | Validating | CocoonHibernation CREATE | Checks `spec.desire` against `{hibernate, wake}`, requires `metadata.name` to equal `spec.podRef.name` (one CR per pod, named after it, so racing duplicate CREATEs collide on name uniqueness), and rejects a CR whose pod already has one — live or still terminating — so two CRs can never fight over one VM. Retargeting an existing CR is blocked by the CRD's CEL rule on `spec.podRef`. See [Validation rules](validation.md). |
| `GET /healthz` | Liveness | — | Always 200 once the binary is running. |

The `/mutate` and `/validate` registrations carry a `namespaceSelector` that
excludes `kube-system`, `kube-node-lease`, `kube-public`, `cocoon-system` and
`cert-manager`, so a webhook outage under `failurePolicy: Fail` cannot block
the pods those namespaces need to recover (the webhook's own included). The
flip side: a cocoon-tolerated pod or workload placed in one of them is neither
gated at creation nor protected against scale-down. Cocoon workloads belong in
ordinary namespaces.
| `GET /readyz` | Readiness | — | Always 200 once the binary is running (liveness-equivalent stub; does not probe apiserver reachability). |
| `GET /metrics` | Prometheus | — | Plain HTTP on `:9090`, separate from the admission TLS port. |

The admission TLS listener reloads its certificate and key from disk
whenever their mtime changes, so a cert-manager rotation lands without
a pod restart.
