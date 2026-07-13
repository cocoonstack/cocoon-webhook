# CocoonSet validation rules

The CRD ships with `+kubebuilder` enum / required / default markers, but
the webhook adds the cross-field business rules that OpenAPI schema
validation cannot express:

- `spec.agent.image` must be set
- `spec.agent.replicas >= 0`
- `spec.agent.mode ∈ {clone, run}`
- `spec.agent.os ∈ {linux, windows, android}`
- `spec.agent.backend ∈ {cloud-hypervisor, firecracker}`
- `spec.agent.connType ∈ {ssh, rdp, vnc, adb}`
- firecracker + `os=windows` is rejected (FC cannot boot Windows guests)
- firecracker + cloudimg URL image is rejected (FC requires OCI images)
- firecracker + `mode=clone` is rejected (FC snapshot/restore freezes guest network state; use `mode=run`)
- `spec.toolboxes[*].name` unique and matches RFC 1123
- `spec.toolboxes[*]` static mode requires both `staticIP` and `staticVMID`
- `spec.toolboxes[*]` non-static modes require `image`
- `spec.toolboxes[*].backend` must match `spec.agent.backend` (static toolboxes skip this check)
- `spec.toolboxes[*]` static-mode entries must declare a valid `connType` (`ssh` / `rdp` / `vnc` / `adb`)
- `spec.snapshotPolicy ∈ {always, main-only, never}`

These rules run on CocoonSet CREATE and UPDATE, behind the
`POST /validate-cocoonset` endpoint — see [Overview](overview.md) for
where that fits among the webhook's other endpoints.
