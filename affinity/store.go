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

func configMapName(pool string) string {
	return configMapPrefix + pool
}

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
	Reserve(ctx context.Context, req ReserveRequest) (Reservation, error)
	Release(ctx context.Context, pool, namespace, deployment string, slot int) error
	List(ctx context.Context, pool string) ([]Reservation, error)
}

type ReserveRequest struct {
	Pool       string
	Namespace  string
	Deployment string // empty for bare pods (pod name itself becomes the VM name)
	PodName    string
}

type NodePicker interface {
	Pick(ctx context.Context, pool string) (string, error)
}

// nilNodePicker returns "" (let the scheduler decide).
type nilNodePicker struct{}

func (nilNodePicker) Pick(_ context.Context, _ string) (string, error) {
	return "", nil
}
