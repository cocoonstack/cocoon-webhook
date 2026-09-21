# Overview

cocoon-webhook is a Kubernetes admission webhook that admits VM-backed pods
only from the CocoonSet controller, rejects scale-down of cocoon workloads,
and validates [CocoonSet](https://github.com/cocoonstack/cocoon-common) and
CocoonHibernation resources beyond what the CRD's OpenAPI schema can express.
It hosts four admission endpoints plus health and metrics surfaces:

| Endpoint | Type | Resources | What it does |
|---|---|---|---|
| `POST /mutate` | Mutating | Pod CREATE / UPDATE | Rejects pods that enter the cocoon gate (the `virtual-kubelet.io/provider` toleration or the `vm.cocoonstack.io/name` annotation) without a CocoonSet owner or without an allowlisted requester identity (`POD_CREATORS`); owner references are client-settable, so the authenticated requester is what actually gates. On UPDATE only a pod that newly acquires the toleration or the annotation is checked, so the operator's and vk-cocoon's runtime patches on gated pods pass untouched. Legitimate pods pass through unmutated. |
| `POST /validate` | Validating | Deployment / StatefulSet UPDATE, including the `deployments/scale` and `statefulsets/scale` subresources | Rejects scale-down on cocoon-tolerated workloads. A `/scale` request — the ordinary `kubectl scale` path — makes the handler GET the parent workload to check its toleration, and it denies fail-closed ("cannot verify parent workload") if that fetch fails. Bypass path for hand-rolled Deployments/StatefulSets carrying the cocoon toleration — the CocoonSet main flow creates Pods directly and does not traverse this endpoint. |
| `POST /validate-cocoonset` | Validating | CocoonSet CREATE / UPDATE | Checks cross-field rules, toolbox name uniqueness, static-mode prerequisites and the derived VM/snapshot name budget. Spec-unchanged updates are skipped so finalizer cleanup remains possible. See [Validation rules](validation.md). |
| `POST /validate-cocoonhibernation` | Validating | CocoonHibernation CREATE | Checks `spec.desire` against `{Hibernate, Wake}`, requires `metadata.name` to equal `spec.podRef.name` (one CR per pod, named after it, so racing duplicate CREATEs collide on name uniqueness), and rejects a CR whose pod already has one — live or still terminating — so two CRs can never fight over one VM. Retargeting an existing CR is blocked by the CRD's CEL rule on `spec.podRef`. See [Validation rules](validation.md). |
| `GET /healthz` | Liveness | — | Always 200 once the binary is running. |
| `GET /readyz` | Readiness | — | Always 200 once the binary is running (liveness-equivalent stub; does not probe apiserver reachability). |
| `GET /metrics` | Prometheus | — | Plain HTTP on `:9090`, separate from the admission TLS port. Exposes `cocoon_webhook_admission_total{handler,result,reason}`, with `handler ∈ {mutate, validate, validate_cocoonset, validate_cocoonhibernation}` and `result ∈ {allow, deny, error, skipped}`. |

The `/mutate` and `/validate` registrations carry a `namespaceSelector` that
excludes `kube-system`, `kube-node-lease`, `kube-public`, `cocoon-system`,
`cert-manager` and `sandbox-system`, so a webhook outage under
`failurePolicy: Fail` cannot block the pods those namespaces need to recover
(the webhook's own included). The
flip side: a cocoon-tolerated pod or workload placed in one of them is neither
gated at creation nor protected against scale-down. Cocoon workloads belong in
ordinary namespaces.

`sandbox-system` is excluded for a different reason. The pod gate keys on the
`virtual-kubelet.io/provider` toleration *key*, and a pod that tolerates a
virtual-kubelet taint with `operator: Exists` carries no value to match on, so
every virtual-kubelet provider's pods look like cocoon VM pods here. vk-sandbox
taints its node with that key and value `sandboxd`, and sandbox-operator's pods
have no CocoonSet owner, so without this exclusion every sandboxd-runtime
Sandbox is denied. Any further provider sharing the key needs the same
treatment. The `/validate-cocoonset` and `/validate-cocoonhibernation`
registrations carry no `namespaceSelector` and gate every namespace, including
`cocoon-system`.

The admission TLS listener reloads its certificate and key from disk
whenever their mtime changes, so a cert-manager rotation lands without
a pod restart.
