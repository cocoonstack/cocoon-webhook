package affinity

import (
	"context"
	"time"
)

const (
	// systemNamespace is where every per-pool affinity ConfigMap
	// lives. The webhook deployment must have RBAC for configmaps in
	// this namespace.
	systemNamespace = "cocoon-system"

	// configMapPrefix prefixes the per-pool ConfigMap name. The pool
	// name is appended verbatim, so cocoonstack.io/pool=gpu becomes
	// "cocoon-affinity-gpu".
	configMapPrefix = "cocoon-affinity-"

	// managedByValue is the value written to the meta.LabelManagedBy
	// label on every per-pool ConfigMap so the reaper can find them.
	managedByValue = "cocoon-webhook"
)

// configMapName returns the ConfigMap name that stores reservations
// for the given cocoon node pool.
func configMapName(pool string) string {
	return configMapPrefix + pool
}

// Reservation is one row in the affinity store: a deployment slot
// pinned to a specific cocoon node. The webhook hands these back to
// callers so they can decide how to translate them into pod patches.
type Reservation struct {
	Pool       string    `json:"pool"`
	Namespace  string    `json:"namespace"`
	Deployment string    `json:"deployment"`
	Slot       int       `json:"slot"`
	VMName     string    `json:"vmName"`
	Pod        string    `json:"pod"`
	Node       string    `json:"node"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Store persists slot-and-node assignments per cocoon node pool.
// Reserve allocates (or reuses) a slot for a deployment pod and pins
// it to a node; Release frees a slot; List returns every reservation
// in the pool for cleanup or inspection.
type Store interface {
	Reserve(ctx context.Context, req ReserveRequest) (Reservation, error)
	Release(ctx context.Context, pool, namespace, deployment string, slot int) error
	List(ctx context.Context, pool string) ([]Reservation, error)
}

// ReserveRequest is the input to Store.Reserve. The fields describe
// enough about the incoming pod that the store can pick a stable
// slot, derive a VM name, and choose a node from the pool.
type ReserveRequest struct {
	Pool       string
	Namespace  string
	Deployment string // empty for bare pods (pod name itself becomes the VM name)
	PodName    string
}

// NodePicker selects a cocoon node from a pool when a fresh
// reservation is being created. Implementations are expected to be
// stateless and read-only against the Kubernetes API.
type NodePicker interface {
	Pick(ctx context.Context, pool string) (string, error)
}

// nilNodePicker is the default NodePicker used when none is supplied.
// It returns the empty node, which translates to "let the scheduler
// decide" downstream — useful for tests that do not care about
// node placement.
type nilNodePicker struct{}

func (nilNodePicker) Pick(_ context.Context, _ string) (string, error) {
	return "", nil
}
