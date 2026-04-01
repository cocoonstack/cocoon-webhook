# cocoon-webhook

`cocoon-webhook` is an optional admission webhook for sticky scheduling of VM-backed pods.

It helps when a workload should return to the same Cocoon worker after restart, because the relevant snapshot or local runtime state is likely to live on that worker already.

## What It Does

For Cocoon-targeted pods, the webhook can:

- derive a stable VM name from the pod owner chain
- look up the worker previously associated with that VM
- patch `spec.nodeName` to keep the pod on the same worker
- write `cocoon.cis/vm-name` when missing

It also validates scale-down behavior for selected workload types so users do not accidentally destroy state by treating VM-backed pods as disposable containers.

## When You Need It

Recommended:

- multi-worker Cocoon pools
- restart-heavy workloads
- deployments that recreate VM-backed pods while expecting state continuity

Often unnecessary:

- single-worker labs
- setups that pin workloads explicitly with `nodeName`
- setups that rely only on `CocoonSet` and already control placement elsewhere

## Relationship to Other Components

- `vk-cocoon` manages the actual VM lifecycle
- `cocoon-operator` exposes `CocoonSet` and `Hibernation`
- `epoch` stores remote snapshots
- `glance` is only a UI consumer and does not depend on this webhook

## Running

The binary serves:

- `POST /mutate`
- `POST /validate`
- `GET /healthz`

This public export does not ship a fixed deployment script. Most users will package the binary behind a standard Kubernetes `Deployment`, `Service`, and `MutatingWebhookConfiguration`, or run it on a control-plane host if that better fits their environment.

## License

MIT
