package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/projecteru2/core/log"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

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
		slot, err := strconv.Atoi(slotStr)
		if err != nil {
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

func reserveAffinity(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod, cm *corev1.ConfigMap) (string, string, error) {
	if clientset == nil {
		return "", "", fmt.Errorf("no clientset available for affinity reservation")
	}
	if pod == nil {
		return "", "", fmt.Errorf("pod is nil")
	}

	var vmName string
	var nodeName string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, getErr := clientset.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, affinityConfigMap, metav1.GetOptions{})
		switch {
		case getErr == nil:
			cm = current
		case apierrors.IsNotFound(getErr):
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      affinityConfigMap,
					Namespace: pod.Namespace,
				},
				Data: map[string]string{},
			}
		default:
			return getErr
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}

		if pod.Annotations != nil && pod.Annotations[vmNameAnnotation] != "" {
			vmName = pod.Annotations[vmNameAnnotation]
		} else {
			vmName = deriveVMName(ctx, pod, pod.Namespace, pod.Name, cm)
		}
		if vmName == "" {
			return fmt.Errorf("empty vm name")
		}

		nodeName = lookupVMNode(cm, vmName)
		if nodeName == "" {
			nodeName = pickAnyCocoonNode(ctx, clientset)
		}
		cm.Data[vmName] = fmt.Sprintf("node:%s,pod:%s", nodeName, pod.Name)

		if apierrors.IsNotFound(getErr) {
			_, err := clientset.CoreV1().ConfigMaps(pod.Namespace).Create(ctx, cm, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, affinityConfigMap, err)
			}
			return err
		}
		_, err := clientset.CoreV1().ConfigMaps(pod.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
		if apierrors.IsNotFound(err) || apierrors.IsAlreadyExists(err) {
			return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, affinityConfigMap, err)
		}
		return err
	})
	if err != nil {
		log.WithFunc("reserveAffinity").Warnf(ctx, "persist affinity %s/%s: %v", pod.Namespace, pod.Name, err)
		return "", "", err
	}
	return vmName, nodeName, nil
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
