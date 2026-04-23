package affinity

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"

	"github.com/cocoonstack/cocoon-common/meta"
)

// ByNodeIndex is the cache indexer name for pod-by-node lookups.
const ByNodeIndex = "byNode"

var _ NodePicker = (*LeastUsedPicker)(nil)

// PodIndexer abstracts the informer cache index lookup used by LeastUsedPicker.
type PodIndexer interface {
	ByIndex(indexName, indexedValue string) ([]any, error)
}

// LeastUsedPicker picks the pool node with the fewest pods, breaking ties alphabetically.
type LeastUsedPicker struct {
	podIndexer PodIndexer
	nodeLister corelisters.NodeLister
}

// NewLeastUsedPicker creates a picker that favors nodes with fewer running pods.
func NewLeastUsedPicker(pods PodIndexer, nodes corelisters.NodeLister) *LeastUsedPicker {
	return &LeastUsedPicker{podIndexer: pods, nodeLister: nodes}
}

// Pick returns "" when the pool has no nodes (let the scheduler decide).
func (p *LeastUsedPicker) Pick(_ context.Context, pool string) (string, error) {
	if pool == "" {
		return "", fmt.Errorf("pick node: pool must not be empty")
	}

	poolSelector := labels.SelectorFromSet(labels.Set{meta.LabelNodePool: pool})
	nodes, err := p.nodeLister.List(poolSelector)
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

	slices.SortStableFunc(nodes, func(a, b *corev1.Node) int {
		return cmp.Or(
			cmp.Compare(counts[a.Name], counts[b.Name]),
			cmp.Compare(a.Name, b.Name),
		)
	})
	return nodes[0].Name, nil
}

// podsPerNode counts live pods (excludes Succeeded/Failed) on each candidate node.
func (p *LeastUsedPicker) podsPerNode(nodes []*corev1.Node) (map[string]int, error) {
	counts := make(map[string]int, len(nodes))
	for _, n := range nodes {
		objs, err := p.podIndexer.ByIndex(ByNodeIndex, n.Name)
		if err != nil {
			return nil, fmt.Errorf("index lookup for node %s: %w", n.Name, err)
		}
		live := 0
		for _, obj := range objs {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				continue
			}
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			live++
		}
		counts[n.Name] = live
	}
	return counts, nil
}

// NodeNameIndexFunc extracts the node name from a Pod for cache indexing.
func NodeNameIndexFunc(obj any) ([]string, error) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil, nil
	}
	if pod.Spec.NodeName == "" {
		return nil, nil
	}
	return []string{pod.Spec.NodeName}, nil
}
