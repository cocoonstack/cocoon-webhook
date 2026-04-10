package affinity

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cocoonstack/cocoon-common/meta"
)

func TestAllocateSlotEmptyEntries(t *testing.T) {
	if got := allocateSlot(nil, "ns", "demo", "demo-0"); got != 0 {
		t.Errorf("empty entries should give slot 0, got %d", got)
	}
}

func TestAllocateSlotReuseExistingPod(t *testing.T) {
	entries := []Reservation{
		{Namespace: "ns", Deployment: "demo", Slot: 0, Pod: "demo-0"},
		{Namespace: "ns", Deployment: "demo", Slot: 1, Pod: "demo-1"},
	}
	if got := allocateSlot(entries, "ns", "demo", "demo-1"); got != 1 {
		t.Errorf("reuse existing slot: got %d, want 1", got)
	}
}

func TestAllocateSlotFillsGap(t *testing.T) {
	entries := []Reservation{
		{Namespace: "ns", Deployment: "demo", Slot: 0, Pod: "demo-0"},
		{Namespace: "ns", Deployment: "demo", Slot: 2, Pod: "demo-2"},
	}
	if got := allocateSlot(entries, "ns", "demo", "demo-new"); got != 1 {
		t.Errorf("fill gap: got %d, want 1", got)
	}
}

func TestAllocateSlotAppendsAtEnd(t *testing.T) {
	entries := []Reservation{
		{Namespace: "ns", Deployment: "demo", Slot: 0, Pod: "demo-0"},
		{Namespace: "ns", Deployment: "demo", Slot: 1, Pod: "demo-1"},
		{Namespace: "ns", Deployment: "demo", Slot: 2, Pod: "demo-2"},
	}
	if got := allocateSlot(entries, "ns", "demo", "demo-new"); got != 3 {
		t.Errorf("append: got %d, want 3", got)
	}
}

func TestAllocateSlotIgnoresOtherDeployments(t *testing.T) {
	entries := []Reservation{
		{Namespace: "ns", Deployment: "other", Slot: 0, Pod: "other-0"},
		{Namespace: "ns", Deployment: "other", Slot: 1, Pod: "other-1"},
	}
	if got := allocateSlot(entries, "ns", "demo", "demo-fresh"); got != 0 {
		t.Errorf("scoping by deployment: got %d, want 0", got)
	}
}

func TestAllocateSlotIgnoresOtherNamespaces(t *testing.T) {
	entries := []Reservation{
		{Namespace: "other", Deployment: "demo", Slot: 0, Pod: "demo-0"},
	}
	if got := allocateSlot(entries, "ns", "demo", "demo-0"); got != 0 {
		t.Errorf("scoping by namespace: got %d, want 0", got)
	}
}

func TestAllocateSlotBarePod(t *testing.T) {
	if got := allocateSlot(nil, "ns", "", "alone"); got != 0 {
		t.Errorf("bare pod always slot 0, got %d", got)
	}
}

func TestLookupExistingNodeMiss(t *testing.T) {
	entries := []Reservation{
		{Namespace: "ns", Deployment: "demo", Slot: 0, Node: "node-a"},
	}
	if got := lookupExistingNode(entries, "ns", "demo", 1); got != "" {
		t.Errorf("miss should be empty, got %q", got)
	}
}

func TestLookupExistingNodeHit(t *testing.T) {
	entries := []Reservation{
		{Namespace: "ns", Deployment: "demo", Slot: 0, Node: "node-a"},
	}
	if got := lookupExistingNode(entries, "ns", "demo", 0); got != "node-a" {
		t.Errorf("hit: got %q, want node-a", got)
	}
}

func TestConfigMapStoreReserveCreatesNewConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewConfigMapStore(client, fixedNodePicker("node-a"))

	res, err := store.Reserve(context.Background(), ReserveRequest{
		Pool:       "default",
		Namespace:  "ns",
		Deployment: "demo",
		PodName:    "demo-0",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if res.VMName != "vk-ns-demo-0" {
		t.Errorf("vmname: %q", res.VMName)
	}
	if res.Node != "node-a" {
		t.Errorf("node: %q", res.Node)
	}
	if res.Slot != 0 {
		t.Errorf("slot: %d", res.Slot)
	}

	cm, err := client.CoreV1().ConfigMaps(systemNamespace).Get(context.Background(), configMapName("default"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cm: %v", err)
	}
	if len(cm.Data) != 1 {
		t.Errorf("expected 1 entry, got %d", len(cm.Data))
	}
	if cm.Labels[meta.LabelNodePool] != "default" {
		t.Errorf("pool label: %q", cm.Labels[meta.LabelNodePool])
	}
}

func TestConfigMapStoreReserveReusesExistingNode(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewConfigMapStore(client, fixedNodePicker("first-node"))

	first, err := store.Reserve(context.Background(), ReserveRequest{
		Pool: "default", Namespace: "ns", Deployment: "demo", PodName: "demo-0",
	})
	if err != nil {
		t.Fatalf("reserve 1: %v", err)
	}

	// Even if the picker would now return a different node, the store
	// should re-use first.Node because slot 0 already has a pin.
	store.Picker = fixedNodePicker("never-picked")
	second, err := store.Reserve(context.Background(), ReserveRequest{
		Pool: "default", Namespace: "ns", Deployment: "demo", PodName: "demo-0",
	})
	if err != nil {
		t.Fatalf("reserve 2: %v", err)
	}
	if second.Node != first.Node {
		t.Errorf("node should be sticky: first=%s second=%s", first.Node, second.Node)
	}
	if second.Slot != first.Slot {
		t.Errorf("slot should be sticky: %d vs %d", first.Slot, second.Slot)
	}
}

func TestConfigMapStoreReleaseRemovesEntry(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewConfigMapStore(client, fixedNodePicker("node-a"))
	ctx := context.Background()
	if _, err := store.Reserve(ctx, ReserveRequest{Pool: "default", Namespace: "ns", Deployment: "demo", PodName: "demo-0"}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := store.Release(ctx, "default", "ns", "demo", 0); err != nil {
		t.Fatalf("release: %v", err)
	}
	entries, err := store.List(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty after release, got %d", len(entries))
	}
}

func TestConfigMapStoreList(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewConfigMapStore(client, fixedNodePicker("node-a"))
	ctx := context.Background()
	for _, name := range []string{"demo-0", "demo-1", "demo-2"} {
		if _, err := store.Reserve(ctx, ReserveRequest{Pool: "default", Namespace: "ns", Deployment: "demo", PodName: name}); err != nil {
			t.Fatalf("reserve %s: %v", name, err)
		}
	}
	entries, err := store.List(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
	// List sorts by namespace, deployment, slot — verify slots are 0, 1, 2 in order.
	for i, e := range entries {
		if e.Slot != i {
			t.Errorf("entry %d slot = %d, want %d", i, e.Slot, i)
		}
	}
}

func TestConfigMapStoreListMissingConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewConfigMapStore(client, nil)
	got, err := store.List(context.Background(), "missing-pool")
	if err != nil {
		t.Fatalf("list missing pool: %v", err)
	}
	if got != nil {
		t.Errorf("missing pool should return nil, got %+v", got)
	}
}

func TestConfigMapStoreReserveBarePod(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewConfigMapStore(client, nil)
	res, err := store.Reserve(context.Background(), ReserveRequest{
		Pool:      "default",
		Namespace: "ns",
		PodName:   "alone",
	})
	if err != nil {
		t.Fatalf("reserve bare: %v", err)
	}
	if res.VMName != "vk-ns-alone" {
		t.Errorf("bare vmname: %q", res.VMName)
	}
	if res.Slot != 0 {
		t.Errorf("bare slot: %d", res.Slot)
	}
}

func TestReservationKey(t *testing.T) {
	if got := reservationKey("ns", "demo", 0); got != "ns/demo/0" {
		t.Errorf("deployed key: %q", got)
	}
	if got := reservationKey("ns", "", 0); got != "ns/_pod/0" {
		t.Errorf("bare key: %q", got)
	}
}

// fixedNodePicker is a NodePicker that always returns the same node.
type fixedNodePicker string

func (n fixedNodePicker) Pick(_ context.Context, _ string) (string, error) {
	return string(n), nil
}

// Sanity check that the fake client behaves the way the test pattern assumes.
func TestFakeClientsetSanity(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: systemNamespace}})
	_, err := client.CoreV1().Namespaces().Get(context.Background(), systemNamespace, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("fake clientset namespace lookup: %v", err)
	}
}
