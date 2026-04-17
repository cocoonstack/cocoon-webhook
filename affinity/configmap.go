package affinity

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/cocoonstack/cocoon-common/meta"
)

var _ Store = (*ConfigMapStore)(nil)

// ConfigMapStore is a Store backed by one ConfigMap per node pool.
// RetryOnConflict guards concurrent webhook replicas.
type ConfigMapStore struct {
	client kubernetes.Interface
	picker NodePicker
}

// NewConfigMapStore creates a ConfigMapStore with the given client and node picker.
func NewConfigMapStore(client kubernetes.Interface, picker NodePicker) *ConfigMapStore {
	if picker == nil {
		picker = nilNodePicker{}
	}
	return &ConfigMapStore{client: client, picker: picker}
}

func (s *ConfigMapStore) Reserve(ctx context.Context, req ReserveRequest) (Reservation, error) {
	if req.Pool == "" {
		return Reservation{}, fmt.Errorf("pool is required")
	}
	if req.Namespace == "" {
		return Reservation{}, fmt.Errorf("namespace is required")
	}
	if req.PodName == "" {
		return Reservation{}, fmt.Errorf("pod name is required")
	}

	var result Reservation
	return result, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, created, err := s.fetchOrInitConfigMap(ctx, req.Pool)
		if err != nil {
			return err
		}

		entries := decodeReservations(cm)

		// Fast path: skip Update if nothing would change.
		if existing, ok := findExistingReservation(entries, req.Namespace, req.Deployment, req.PodName); ok && existing.Node != "" {
			result = existing
			return nil
		}

		slot := allocateSlot(entries, req.Namespace, req.Deployment, req.PodName)

		var vmName string
		if req.Deployment != "" {
			vmName = meta.VMNameForDeployment(req.Namespace, req.Deployment, slot)
		} else {
			vmName = meta.VMNameForPod(req.Namespace, req.PodName)
		}

		node := lookupExistingNode(entries, req.Namespace, req.Deployment, slot)
		if node == "" {
			node, err = s.picker.Pick(ctx, req.Pool)
			if err != nil {
				return fmt.Errorf("pick node: %w", err)
			}
		}

		result = Reservation{
			Pool:       req.Pool,
			Namespace:  req.Namespace,
			Deployment: req.Deployment,
			Slot:       slot,
			VMName:     vmName,
			Pod:        req.PodName,
			Node:       node,
			UpdatedAt:  time.Now().UTC(),
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return fmt.Errorf("encode reservation: %w", marshalErr)
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[reservationKey(req.Namespace, req.Deployment, slot)] = string(raw)

		return s.persist(ctx, cm, created)
	})
}

func (s *ConfigMapStore) Release(ctx context.Context, pool, namespace, deployment string, slot int) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, _, err := s.fetchOrInitConfigMap(ctx, pool)
		if err != nil {
			return err
		}
		key := reservationKey(namespace, deployment, slot)
		if _, ok := cm.Data[key]; !ok {
			return nil
		}
		delete(cm.Data, key)
		return s.persist(ctx, cm, false)
	})
}

func (s *ConfigMapStore) List(ctx context.Context, pool string) ([]Reservation, error) {
	cm, err := s.client.CoreV1().ConfigMaps(systemNamespace).Get(ctx, configMapName(pool), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := decodeReservations(cm)
	slices.SortFunc(out, func(a, b Reservation) int {
		return cmp.Or(
			cmp.Compare(a.Namespace, b.Namespace),
			cmp.Compare(a.Deployment, b.Deployment),
			cmp.Compare(a.Slot, b.Slot),
		)
	})
	return out, nil
}

func (s *ConfigMapStore) fetchOrInitConfigMap(ctx context.Context, pool string) (*corev1.ConfigMap, bool, error) {
	name := configMapName(pool)
	cm, err := s.client.CoreV1().ConfigMaps(systemNamespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		return cm, false, nil
	case apierrors.IsNotFound(err):
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: systemNamespace,
				Labels: map[string]string{
					meta.LabelManagedBy: managedByValue,
					meta.LabelNodePool:  pool,
				},
			},
			Data: map[string]string{},
		}, true, nil
	default:
		return nil, false, fmt.Errorf("get configmap %s: %w", name, err)
	}
}

func (s *ConfigMapStore) persist(ctx context.Context, cm *corev1.ConfigMap, isNew bool) error {
	cms := s.client.CoreV1().ConfigMaps(systemNamespace)
	if isNew {
		if _, err := cms.Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Convert to Conflict so RetryOnConflict re-fetches.
				return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, cm.Name, err)
			}
			return fmt.Errorf("create configmap %s: %w", cm.Name, err)
		}
		return nil
	}
	if _, err := cms.Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update configmap %s: %w", cm.Name, err)
	}
	return nil
}

// reservationKey uses "." as separator (not "/") because ConfigMap keys reject "/".
func reservationKey(namespace, deployment string, slot int) string {
	if deployment == "" {
		return namespace + "._pod." + strconv.Itoa(slot)
	}
	return namespace + "." + deployment + "." + strconv.Itoa(slot)
}

func decodeReservations(cm *corev1.ConfigMap) []Reservation {
	if cm == nil || len(cm.Data) == 0 {
		return nil
	}
	out := make([]Reservation, 0, len(cm.Data))
	for _, raw := range cm.Data {
		var r Reservation
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// allocateSlot: reuse existing pod's slot, fill smallest gap, or append.
// Bare pods always get slot 0.
func allocateSlot(entries []Reservation, namespace, deployment, podName string) int {
	if deployment == "" {
		return 0
	}
	used := map[int]string{}
	maxSlot := -1
	for _, e := range entries {
		if e.Namespace != namespace || e.Deployment != deployment {
			continue
		}
		used[e.Slot] = e.Pod
		if e.Slot > maxSlot {
			maxSlot = e.Slot
		}
	}
	for slot, pod := range used {
		if pod == podName {
			return slot
		}
	}
	for i := 0; i <= maxSlot; i++ {
		if _, taken := used[i]; !taken {
			return i
		}
	}
	return maxSlot + 1
}

func lookupExistingNode(entries []Reservation, namespace, deployment string, slot int) string {
	for _, e := range entries {
		if e.Namespace == namespace && e.Deployment == deployment && e.Slot == slot {
			return e.Node
		}
	}
	return ""
}

func findExistingReservation(entries []Reservation, namespace, deployment, podName string) (Reservation, bool) {
	for _, e := range entries {
		if e.Namespace == namespace && e.Deployment == deployment && e.Pod == podName {
			return e, true
		}
	}
	return Reservation{}, false
}
