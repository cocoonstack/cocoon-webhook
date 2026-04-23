package affinity

import (
	"context"
	"time"
)

const (
	systemNamespace = "cocoon-system"
	configMapPrefix = "cocoon-affinity-"
	managedByValue  = "cocoon-webhook"
)

// Reservation records a slot-and-node assignment for a single pod.
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
type Store interface {
	// Reserve allocates (or reuses) a slot for the request and returns the resulting Reservation.
	Reserve(ctx context.Context, req ReserveRequest) (Reservation, error)
	// Release removes the reservation identified by pool, namespace, deployment, and slot.
	Release(ctx context.Context, pool, namespace, deployment string, slot int) error
	// List returns all reservations currently held for the given pool.
	List(ctx context.Context, pool string) ([]Reservation, error)
}

// ReserveRequest contains the parameters for a reservation attempt.
type ReserveRequest struct {
	Pool       string
	Namespace  string
	Deployment string // empty for bare pods (pod name itself becomes the VM name)
	PodName    string
}

// NodePicker selects a target node within a pool for pod scheduling.
type NodePicker interface {
	Pick(ctx context.Context, pool string) (string, error)
}

// nilNodePicker returns "" (let the scheduler decide).
type nilNodePicker struct{}

// Pick always returns an empty node name.
func (nilNodePicker) Pick(_ context.Context, _ string) (string, error) {
	return "", nil
}

func configMapName(pool string) string {
	return configMapPrefix + pool
}
