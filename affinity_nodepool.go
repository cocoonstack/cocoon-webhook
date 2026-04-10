package main

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// nodePoolLabel selects cocoon nodes by pool. The webhook uses
	// the label value to scope its node picker; node operators are
	// expected to set this label on every cocoon-pool node.
	nodePoolLabel = "cocoonstack.io/pool"
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
		LabelSelector: fmt.Sprintf("%s=%s", nodePoolLabel, pool),
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

// podsPerNode counts the pods currently scheduled to each candidate
// node. Excluded: pods in Succeeded / Failed phases (they no longer
// consume capacity).
func (p *LeastUsedPicker) podsPerNode(ctx context.Context, nodes []corev1.Node) (map[string]int, error) {
	counts := map[string]int{}
	for _, node := range nodes {
		counts[node.Name] = 0
		pods, err := p.Client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("spec.nodeName=%s", node.Name),
		})
		if err != nil {
			return nil, fmt.Errorf("list pods on node %s: %w", node.Name, err)
		}
		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			counts[node.Name]++
		}
	}
	return counts, nil
}
