# cocoon-webhook

Kubernetes admission webhook for the
[cocoonstack](https://github.com/cocoonstack) VM platform. It admits
VM-backed pods only from the CocoonSet controller, rejects scale-down of
cocoon Deployments and StatefulSets, validates CocoonSet CRs against
cross-field business rules the CRD's OpenAPI schema cannot express, and
pins each pod to at most one live CocoonHibernation.

## Guides

- [Overview](overview.md) — the admission endpoints and what each one does
- [Validation rules](validation.md) — the cross-field business rules enforced on CocoonSet CREATE/UPDATE and on CocoonHibernation CREATE
- [Configuration](configuration.md) — every environment variable
- [Installation](installation.md) — the `kubectl apply -k` path and building from source

## Repository

Source and issue tracker:
[github.com/cocoonstack/cocoon-webhook](https://github.com/cocoonstack/cocoon-webhook).
Part of the [cocoonstack](https://cocoonstack.github.io/) MicroVM platform.
