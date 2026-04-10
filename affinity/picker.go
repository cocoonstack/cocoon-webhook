package affinity

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"

	"github.com/cocoonstack/cocoon-common/meta"
)

var _ NodePicker = (*LeastUsedPicker)(nil)

// LeastUsedPicker picks the cocoon node in a pool that currently
// hosts the fewest pods. Ties are broken alphabetically so the
// outcome is deterministic across multiple webhook replicas.
//
// Both the node lookup (by pool label) and the pod count are served
// from shared informer caches, so a Pick is a couple of in-memory
// scans rather than two apiserver round trips on the admission hot
// path.
type LeastUsedPicker struct {
	PodLister  corelisters.PodLister
	NodeLister corelisters.NodeLister
}

// NewLeastUsedPicker constructs a LeastUsedPicker that reads from
// the supplied informer-backed listers. Callers are responsible for
// starting the informer factory and waiting for cache sync before
// calling Pick.
func NewLeastUsedPicker(pods corelisters.PodLister, nodes corelisters.NodeLister) *LeastUsedPicker {
	return &LeastUsedPicker{PodLister: pods, NodeLister: nodes}
}

// Pick returns the name of the cocoon node in the pool that has the
// fewest pods scheduled to it. Returns "" (with no error) when the
// pool has no nodes — callers should treat that as "let the
// scheduler decide".
func (p *LeastUsedPicker) Pick(_ context.Context, pool string) (string, error) {
	if pool == "" {
		return "", fmt.Errorf("pool is required")
	}

	poolSelector := labels.SelectorFromSet(labels.Set{meta.LabelNodePool: pool})
	nodes, err := p.NodeLister.List(poolSelector)
	if err != nil {
		return "", fmt.Errorf("list nodes for pool %s: %w", pool, err)
	}
	if len(nodes) == 0 {
		return "", nil
	}

	counts, err := p.podsPerNode(nodes)
	if err != nil {
		return "", err
	}

	sort.SliceStable(nodes, func(i, j int) bool {
		ci, cj := counts[nodes[i].Name], counts[nodes[j].Name]
		if ci != cj {
			return ci < cj
		}
		return nodes[i].Name < nodes[j].Name
	})
	return nodes[0].Name, nil
}

// podsPerNode counts the live pods currently scheduled to each
// candidate node. Excluded: pods in Succeeded / Failed phases (they
// no longer consume capacity).
//
// The PodLister.List(labels.Everything()) call walks the informer's
// in-memory cache — no apiserver round trip — so this is cheap on
// the hot path.
func (p *LeastUsedPicker) podsPerNode(nodes []*corev1.Node) (map[string]int, error) {
	counts := make(map[string]int, len(nodes))
	for _, n := range nodes {
		counts[n.Name] = 0
	}

	pods, err := p.PodLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("list pods from cache: %w", err)
	}
	for _, pod := range pods {
		if _, ok := counts[pod.Spec.NodeName]; !ok {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		counts[pod.Spec.NodeName]++
	}
	return counts, nil
}
