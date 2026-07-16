# cocoon-webhook

Kubernetes admission webhook for the [cocoonstack](https://github.com/cocoonstack) VM platform.

cocoon-webhook hosts four admission endpoints: a mutating webhook that
rejects cocoon-tolerated pods not owned by a CocoonSet, a validating
webhook that rejects scale-down on cocoon-tolerated Deployments/
StatefulSets, a validating webhook that enforces CocoonSet cross-field
business rules the CRD's OpenAPI schema can't express, and a validating
webhook that pins each pod to at most one live CocoonHibernation.

## Documentation

- [Overview](docs/overview.md) — the admission endpoints and what each one does
- [CocoonSet validation rules](docs/validation.md) — the cross-field business rules enforced on CocoonSet CREATE/UPDATE
- [Configuration](docs/configuration.md) — every environment variable
- [Installation](docs/installation.md) — the `kubectl apply -k` path and building from source

## Development

```bash
make all            # full pipeline: deps + fmt + lint + test + build
make build          # build cocoon-webhook binary
make test           # vet + race-detected tests
make lint           # golangci-lint on linux + darwin
make fmt            # gofumpt + goimports
make help           # show all targets
```

The Makefile detects Go workspace mode (`go env GOWORK`) and skips `go mod tidy` when active so cross-module references resolve through `go.work` without forcing a release of cocoon-common.

## Related projects

| Project | Role |
|---|---|
| [cocoon-common](https://github.com/cocoonstack/cocoon-common) | CRD types, annotation contract, shared helpers |
| [cocoon-operator](https://github.com/cocoonstack/cocoon-operator) | CocoonSet and CocoonHibernation reconcilers |
| [epoch](https://github.com/cocoonstack/epoch) | Snapshot registry and storage backend |
| [vk-cocoon](https://github.com/cocoonstack/vk-cocoon) | Virtual kubelet provider managing VM lifecycle |

## License

[MIT](LICENSE)
