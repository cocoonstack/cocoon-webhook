package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

// ConfigMapStore is an AffinityStore backed by one ConfigMap per
// cocoon node pool. Each entry is keyed by "<namespace>/<deployment>/<slot>"
// and the value is a JSON-encoded Reservation. RetryOnConflict
// guards concurrent webhook replicas racing on the same ConfigMap.
type ConfigMapStore struct {
	Client kubernetes.Interface
	Picker NodePicker
}

// NewConfigMapStore returns a ConfigMapStore using nilNodePicker as
// the default NodePicker if none is supplied.
func NewConfigMapStore(client kubernetes.Interface, picker NodePicker) *ConfigMapStore {
	if picker == nil {
		picker = nilNodePicker{}
	}
	return &ConfigMapStore{Client: client, Picker: picker}
}

// Reserve allocates (or reuses) a slot for the requested pod and
// pins it to a cocoon node. Bare pods (req.Deployment == "") get a
// degenerate "slot 0" reservation keyed by pod name only.
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
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, created, err := s.fetchOrInitConfigMap(ctx, req.Pool)
		if err != nil {
			return err
		}

		entries := decodeReservations(cm)
		slot := allocateSlot(entries, req.Namespace, req.Deployment, req.PodName)

		var vmName string
		if req.Deployment != "" {
			vmName = meta.VMNameForDeployment(req.Namespace, req.Deployment, slot)
		} else {
			vmName = meta.VMNameForPod(req.Namespace, req.PodName)
		}

		// Reuse the existing node if a previous reservation pinned this
		// (deployment, slot) to one. Otherwise consult the picker.
		node := lookupExistingNode(entries, req.Namespace, req.Deployment, slot)
		if node == "" {
			node, err = s.Picker.Pick(ctx, req.Pool)
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
	if err != nil {
		return Reservation{}, err
	}
	return result, nil
}

// Release deletes a single reservation entry. Missing entries return
// nil so callers can release optimistically.
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

// List returns every reservation currently stored for the pool.
func (s *ConfigMapStore) List(ctx context.Context, pool string) ([]Reservation, error) {
	cm, err := s.Client.CoreV1().ConfigMaps(affinitySystemNamespace).Get(ctx, affinityConfigMapName(pool), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := decodeReservations(cm)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Deployment != out[j].Deployment {
			return out[i].Deployment < out[j].Deployment
		}
		return out[i].Slot < out[j].Slot
	})
	return out, nil
}

func (s *ConfigMapStore) fetchOrInitConfigMap(ctx context.Context, pool string) (*corev1.ConfigMap, bool, error) {
	name := affinityConfigMapName(pool)
	cm, err := s.Client.CoreV1().ConfigMaps(affinitySystemNamespace).Get(ctx, name, metav1.GetOptions{})
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
				Namespace: affinitySystemNamespace,
				Labels: map[string]string{
					meta.LabelManagedBy: "cocoon-webhook",
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
	cms := s.Client.CoreV1().ConfigMaps(affinitySystemNamespace)
	if isNew {
		if _, err := cms.Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Convert to a Conflict so retry.RetryOnConflict
				// loops back through fetchOrInitConfigMap on the
				// next attempt.
				return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, cm.Name, err)
			}
			return fmt.Errorf("create configmap %s: %w", cm.Name, err)
		}
		return nil
	}
	// retry.RetryOnConflict matches via apierrors.IsConflict even
	// when the error is wrapped, so a single fmt.Errorf preserves
	// both the message and the retry signal.
	if _, err := cms.Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update configmap %s: %w", cm.Name, err)
	}
	return nil
}

// reservationKey is the per-entry key used inside the ConfigMap data
// map. Bare pods omit the deployment segment.
func reservationKey(namespace, deployment string, slot int) string {
	if deployment == "" {
		return namespace + "/_pod/" + strconv.Itoa(slot)
	}
	return namespace + "/" + deployment + "/" + strconv.Itoa(slot)
}

// decodeReservations parses every entry of a ConfigMap into a slice
// of Reservation values, ignoring malformed rows. Callers should
// treat the result as a snapshot for the duration of one Reserve.
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

// allocateSlot picks a slot index for the requesting pod. The
// algorithm:
//
//  1. If a reservation already exists for (namespace, deployment, podName), reuse its slot.
//  2. Otherwise fill the smallest unused slot in the contiguous range [0, maxSlot+1).
//  3. Otherwise return maxSlot+1 (append a new slot).
//
// Bare pods (deployment == "") always get slot 0; their reservation
// key includes the pod name so they never collide with each other.
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

// lookupExistingNode returns the node a previous reservation pinned
// to (namespace, deployment, slot), or "" if none exists. The webhook
// always reuses the previous node so the same VM lands on the same
// host across pod recreations.
func lookupExistingNode(entries []Reservation, namespace, deployment string, slot int) string {
	for _, e := range entries {
		if e.Namespace == namespace && e.Deployment == deployment && e.Slot == slot {
			return e.Node
		}
	}
	return ""
}
