# cocoon-webhook

Kubernetes admission webhook for the
[cocoonstack](https://github.com/cocoonstack) VM platform. It enforces
cocoon sticky scheduling on Pod, Deployment, and StatefulSet admission,
and validates CocoonSet CRs against cross-field business rules the
CRD's OpenAPI schema cannot express.

## Guides

- [Overview](overview.md) — the admission endpoints and what each one does
- [CocoonSet validation rules](validation.md) — the cross-field business rules enforced on CocoonSet CREATE/UPDATE
- [Configuration](configuration.md) — every environment variable
- [Installation](installation.md) — the `kubectl apply -k` path and building from source
