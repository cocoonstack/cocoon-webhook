# Overview

cocoon-webhook is a Kubernetes admission webhook that enforces cocoon
sticky scheduling and validates [CocoonSet](https://github.com/cocoonstack/cocoon-operator)
resources beyond what the CRD's OpenAPI schema can express. It hosts
four admission endpoints plus health and metrics surfaces:

| Endpoint | Type | Resources | What it does |
|---|---|---|---|
| `POST /mutate` | Mutating | Pod CREATE | Rejects cocoon-tolerated pods that are not owned by a CocoonSet or not created by an allowlisted controller identity (`POD_CREATORS`); owner references are client-settable, so the authenticated requester is what actually gates. Legitimate pods pass through unmutated. |
| `POST /validate` | Validating | Deployment / StatefulSet UPDATE | Rejects scale-down on cocoon-tolerated workloads. Bypass path for hand-rolled Deployments/StatefulSets carrying the cocoon toleration — the CocoonSet main flow creates Pods directly and does not traverse this endpoint. |
| `POST /validate-cocoonset` | Validating | CocoonSet CREATE / UPDATE | Catches the cross-field business rules the CRD's OpenAPI schema cannot express (image required, toolbox name uniqueness, static-mode prerequisites). See [CocoonSet validation rules](validation.md). |
| `POST /validate-cocoonhibernation` | Validating | CocoonHibernation CREATE | Rejects a second live CocoonHibernation targeting a pod that already has one, so two CRs with opposing desires can never fight over one VM. Retargeting an existing CR is blocked by the CRD's CEL rule on `spec.podRef`. |
| `GET /healthz` | Liveness | — | Always 200 once the binary is running. |
| `GET /readyz` | Readiness | — | Always 200 once the binary is running (liveness-equivalent stub; does not probe apiserver reachability). |
| `GET /metrics` | Prometheus | — | Plain HTTP on `:9090`, separate from the admission TLS port. |

The admission TLS listener reloads its certificate and key from disk
whenever their mtime changes, so a cert-manager rotation lands without
a pod restart.
