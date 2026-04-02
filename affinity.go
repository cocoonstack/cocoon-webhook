package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/cocoonstack/cocoon-common/meta"
)

// deriveVMName creates a stable VM name from the pod's owner chain.
// For Deployment pods: vk-{ns}-{deployment-name}-{slot}
// For bare pods: vk-{ns}-{pod-name}
func deriveVMName(ctx context.Context, pod *corev1.Pod, ns, podName string, cm *corev1.ConfigMap) string {
	deployName := meta.OwnerDeploymentName(pod.OwnerReferences)
	if deployName != "" {
		slot, err := allocateSlot(ns, deployName, podName, cm)
		if err == nil {
			return meta.VMNameForDeployment(ns, deployName, slot)
		}
		logWarnAllocateSlot(ctx, ns, deployName, err)
	}

	return meta.VMNameForPod(ns, podName)
}

// allocateSlot assigns a stable replica index for a Deployment pod.
// Reads the affinity ConfigMap to track slot assignments.
func allocateSlot(ns, deployName, podName string, cm *corev1.ConfigMap) (int, error) {
	if cm == nil {
		return 0, fmt.Errorf("no ConfigMap available for slot allocation")
	}

	prefix := fmt.Sprintf("vk-%s-%s-", ns, deployName)
	usedSlots := map[int]string{}
	maxSlot := -1

	for key, val := range cm.Data {
		slotStr, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		slot := 0
		if _, err := fmt.Sscanf(slotStr, "%d", &slot); err != nil {
			continue
		}
		if pn := parseConfigMapField(val, "pod"); pn != "" {
			usedSlots[slot] = pn
		}
		if slot > maxSlot {
			maxSlot = slot
		}
	}

	for slot, pn := range usedSlots {
		if pn == podName {
			return slot, nil
		}
	}

	for i := 0; i <= maxSlot; i++ {
		if _, used := usedSlots[i]; !used {
			return i, nil
		}
	}

	return maxSlot + 1, nil
}

func lookupVMNode(cm *corev1.ConfigMap, vmName string) string {
	if cm == nil {
		return ""
	}
	val, ok := cm.Data[vmName]
	if !ok {
		return ""
	}
	return parseConfigMapField(val, "node")
}

func pickAnyCocoonNode(ctx context.Context, clientset kubernetes.Interface) string {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}

	cocoonNodes := make([]string, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		if n.Labels["type"] == "virtual-kubelet" {
			cocoonNodes = append(cocoonNodes, n.Name)
			continue
		}
		for _, taint := range n.Spec.Taints {
			if taint.Key == meta.TolerationKey {
				cocoonNodes = append(cocoonNodes, n.Name)
				break
			}
		}
	}
	if len(cocoonNodes) == 0 {
		return ""
	}
	slices.Sort(cocoonNodes)
	return cocoonNodes[int(metav1.Now().UnixNano())%len(cocoonNodes)]
}
