# Validation rules

## CocoonSet

The CRD ships with `+kubebuilder` enum / required / default markers, but
the webhook adds the cross-field business rules that OpenAPI schema
validation cannot express:

- `spec.agent.image` must be set
- `spec.agent.replicas >= 0`
- `spec.agent.mode ∈ {clone, run}`, defaulting to `clone` when unset
- `spec.agent.os ∈ {linux, windows, android, macos}`
- `spec.agent.backend ∈ {cloud-hypervisor, firecracker}`, defaulting to `cloud-hypervisor` when unset
- `spec.agent.connType ∈ {ssh, rdp, vnc, adb}`
- firecracker + `os=windows` is rejected (FC cannot boot Windows guests)
- firecracker + cloudimg URL image is rejected (FC requires OCI images)
- firecracker + `mode=clone` is rejected, including an unset mode, which defaults to `clone` (FC snapshot/restore freezes guest network state; use `mode=run`)
- `spec.toolboxes[*].name` is required, unique, matches RFC 1123, and is not purely numeric (reserved for agent slot indexing)
- `spec.toolboxes[*].mode ∈ {run, clone, static}`, defaulting to `run` when unset — so an unset toolbox mode does not trip the firecracker+clone rule above
- `spec.toolboxes[*]` static mode requires both `staticIP` and `staticVMID`
- `spec.toolboxes[*]` non-static modes require `image`, and get the same `os`/`connType`/`backend` enum checks and firecracker-Windows/cloudimg-URL rules as `spec.agent`
- `spec.toolboxes[*].backend` must match `spec.agent.backend` (static toolboxes skip this check)
- `spec.toolboxes[*]` static-mode entries: `connType` may be left unset (falls back to OS-based inference: Linux→ssh, Windows→rdp, Android→adb); a non-empty value must be one of `ssh` / `rdp` / `vnc` / `adb`
- clone-mode images (`spec.agent.image`, `spec.toolboxes[*].image`) must be a relative `repo[:tag]` with a lowercase repo path — registry hosts and ports, digests, and uppercase repo characters are rejected (the tag keeps the OCI tag character set), because the snapshot pull path resolves images under the org registry base and has no external-ref fallback
- `spec.snapshotPolicy ∈ {always, main-only, never}`
- `spec.hibernatePolicy ∈ {retain, release}`

These rules run on CocoonSet CREATE, and on UPDATE only when the spec
changed — a spec-unchanged UPDATE (a finalizer or metadata patch) is
skipped, so an invalid CR that predates stricter validation stays
deletable. They run behind the `POST /validate-cocoonset` endpoint —
see [Overview](overview.md) for where that fits among the webhook's
other endpoints.

## CocoonHibernation

`POST /validate-cocoonhibernation` gates CREATE only (an UPDATE is skipped —
retargeting `spec.podRef` is already blocked by the CRD's CEL rule):

- `spec.desire ∈ {Hibernate, Wake}`
- `metadata.name` must equal `spec.podRef.name`, so two racing CREATEs for one
  pod collide on apiserver name uniqueness instead of both being admitted
- the pod must not already have a CocoonHibernation — including one that is
  still terminating, whose pending finalizer will delete that pod's hibernate
  snapshot. Flip `spec.desire` on the existing CR instead of creating a second
- the duplicate check fails closed: if the LIST cannot be served, the CREATE is
  denied rather than risk the non-converging hibernate/wake flip-flop that two
  CRs over one VM produce
