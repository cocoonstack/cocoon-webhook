# cocoon-webhook

Kubernetes admission webhook for the [cocoonstack](https://github.com/cocoonstack)
VM platform, enforcing controller ownership and lifecycle constraints before
resources reach the VM controllers.

**Documentation: [cocoonstack.github.io/cocoon-webhook](https://cocoonstack.github.io/cocoon-webhook/)** (source in [`docs/`](docs/)).

## Highlights

- Pods entering the cocoon gate through the `virtual-kubelet.io/provider`
  toleration or the `vm.cocoonstack.io/name` annotation require a CocoonSet owner
  and an allowlisted requester. This applies on CREATE and on UPDATE from outside
  the gate; updates to already gated Pods remain allowed.
- Scale-down is blocked on cocoon-tolerated Deployments and StatefulSets.
- CocoonSet validation checks cross-field rules and reserves room for derived
  snapshot names before a VM is created.
- CocoonHibernation validation allows at most one live object per Pod.

## Related projects

| Project | Role |
|---|---|
| [cocoon-common](https://github.com/cocoonstack/cocoon-common) | CRD types, annotation contract, shared helpers |
| [cocoon-operator](https://github.com/cocoonstack/cocoon-operator) | CocoonSet and CocoonHibernation reconcilers |
| [vk-cocoon](https://github.com/cocoonstack/vk-cocoon) | Virtual kubelet provider managing VM lifecycle |

## Development

```bash
make build          # build cocoon-webhook binary
make test           # vet + race-detected tests
make lint           # golangci-lint on linux + darwin
make fmt            # gofumpt + goimports
```

## License

[MIT](LICENSE)
