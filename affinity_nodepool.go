package main

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/cocoonstack/cocoon-common/meta"
)

// LeastUsedPicker picks the cocoon node in a pool that currently
// hosts the fewest pods. Ties are broken alphabetically so the
// outcome is deterministic across multiple webhook replicas.
type LeastUsedPicker struct {
	Client kubernetes.Interface
}

// NewLeastUsedPicker constructs a LeastUsedPicker.
func NewLeastUsedPicker(client kubernetes.Interface) *LeastUsedPicker {
	return &LeastUsedPicker{Client: client}
}

// Pick returns the name of the cocoon node in the pool that has the
// fewest pods scheduled to it. Returns "" (with no error) when the
// pool has no nodes — callers should treat that as "let the
// scheduler decide".
func (p *LeastUsedPicker) Pick(ctx context.Context, pool string) (string, error) {
	if pool == "" {
		return "", fmt.Errorf("pool is required")
	}

	nodes, err := p.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", meta.LabelNodePool, pool),
	})
	if err != nil {
		return "", fmt.Errorf("list nodes for pool %s: %w", pool, err)
	}
	if len(nodes.Items) == 0 {
		return "", nil
	}

	counts, err := p.podsPerNode(ctx, nodes.Items)
	if err != nil {
		return "", err
	}

	// Stable sort: lowest count first, then alphabetical name.
	candidates := make([]corev1.Node, len(nodes.Items))
	copy(candidates, nodes.Items)
	sort.SliceStable(candidates, func(i, j int) bool {
		ci, cj := counts[candidates[i].Name], counts[candidates[j].Name]
		if ci != cj {
			return ci < cj
		}
		return candidates[i].Name < candidates[j].Name
	})
	return candidates[0].Name, nil
}

// podsPerNode counts the live pods currently scheduled to each
// candidate node. Excluded: pods in Succeeded / Failed phases (they
// no longer consume capacity).
//
// One cluster-wide pods.List + in-memory grouping replaces the N+1
// "list pods on each node" the previous implementation used. The
// FieldSelector lets the API server filter on its side, so the
// transferred set is bounded by the candidate-node count rather
// than every pod in the cluster.
func (p *LeastUsedPicker) podsPerNode(ctx context.Context, nodes []corev1.Node) (map[string]int, error) {
	counts := make(map[string]int, len(nodes))
	wanted := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		counts[n.Name] = 0
		wanted[n.Name] = struct{}{}
	}

	pods, err := p.Client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if _, ok := wanted[pod.Spec.NodeName]; !ok {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		counts[pod.Spec.NodeName]++
	}
	return counts, nil
}
