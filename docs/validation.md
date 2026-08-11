# Validation rules

## CocoonSet

The CRD ships with `+kubebuilder` enum / required / default markers, but
the webhook adds the cross-field business rules that OpenAPI schema
validation cannot express:

- `spec.agent.image` must be set
- `spec.agent.replicas >= 0`
- `spec.agent.mode ∈ {clone, run}`
- `spec.agent.os ∈ {linux, windows, android, macos}`
- `spec.agent.backend ∈ {cloud-hypervisor, firecracker}`
- `spec.agent.connType ∈ {ssh, rdp, vnc, adb}`
- firecracker + `os=windows` is rejected (FC cannot boot Windows guests)
- firecracker + cloudimg URL image is rejected (FC requires OCI images)
- firecracker + `mode=clone` is rejected (FC snapshot/restore freezes guest network state; use `mode=run`)
- `spec.toolboxes[*].name` unique, matches RFC 1123, and is not purely numeric (reserved for agent slot indexing)
- `spec.toolboxes[*].mode ∈ {run, clone, static}`
- `spec.toolboxes[*]` static mode requires both `staticIP` and `staticVMID`
- `spec.toolboxes[*]` non-static modes require `image`
- `spec.toolboxes[*].backend` must match `spec.agent.backend` (static toolboxes skip this check)
- `spec.toolboxes[*]` static-mode entries must declare a valid `connType` (`ssh` / `rdp` / `vnc` / `adb`)
- clone-mode images (`spec.agent.image`, `spec.toolboxes[*].image`) must be `repo[:tag]` — registry ports and digests are rejected, because the snapshot pull path resolves images under the org registry base and has no external-ref fallback
- `spec.snapshotPolicy ∈ {always, main-only, never}`
- `spec.hibernatePolicy ∈ {retain, release}`

These rules run on CocoonSet CREATE and UPDATE, behind the
`POST /validate-cocoonset` endpoint — see [Overview](overview.md) for
where that fits among the webhook's other endpoints.

## CocoonHibernation

`POST /validate-cocoonhibernation` gates CREATE only (an UPDATE is skipped —
retargeting `spec.podRef` is already blocked by the CRD's CEL rule):

- `spec.desire ∈ {hibernate, wake}`
- `metadata.name` must equal `spec.podRef.name`, so two racing CREATEs for one
  pod collide on apiserver name uniqueness instead of both being admitted
- the pod must not already have a CocoonHibernation — including one that is
  still terminating, whose pending finalizer will delete that pod's hibernate
  snapshot. Flip `spec.desire` on the existing CR instead of creating a second
- the duplicate check fails closed: if the LIST cannot be served, the CREATE is
  denied rather than risk the non-converging hibernate/wake flip-flop that two
  CRs over one VM produce
