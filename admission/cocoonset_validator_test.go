package admission

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
)

func TestValidateCocoonSetSpec(t *testing.T) {
	storage100Gi := resource.MustParse("100Gi")

	tests := []struct {
		name         string
		cs           *cocoonv1.CocoonSet
		wantContains []string
	}{
		{
			name: "accepts minimal spec",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "ghcr.io/cocoonstack/cocoon/ubuntu:24.04"},
			}},
		},
		{
			name:         "rejects missing image",
			cs:           &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{}},
			wantContains: []string{"spec.agent.image"},
		},
		{
			name: "rejects negative replicas",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x", Replicas: -1},
			}},
			wantContains: []string{"replicas must be >= 0"},
		},
		{
			name: "rejects bad agent mode",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x", Mode: "ouija"},
			}},
			wantContains: []string{"agent.mode"},
		},
		{
			name: "rejects clone-mode digest image",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "ubuntu@sha256:deadbeef"},
			}},
			wantContains: []string{"spec.agent.image", "must be repo[:tag]"},
		},
		{
			name: "rejects clone-mode registry-port image",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "registry:5000/ubuntu:24.04"},
			}},
			wantContains: []string{"spec.agent.image", "must be repo[:tag]"},
		},
		{
			name: "accepts run-mode digest image",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "ubuntu@sha256:deadbeef", Mode: cocoonv1.AgentModeRun},
			}},
		},
		{
			name: "rejects clone-mode toolbox registry-port image",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{
					{Name: "tb", Mode: cocoonv1.ToolboxModeClone, Image: "registry:5000/tools:v1"},
				},
			}},
			wantContains: []string{"spec.toolboxes[0].image", "must be repo[:tag]"},
		},
		{
			name: "rejects duplicate toolbox names",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{
					{Name: "tb", Image: "y"},
					{Name: "tb", Image: "z"},
				},
			}},
			wantContains: []string{"duplicates an earlier toolbox"},
		},
		{
			name: "rejects bad toolbox name",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent:     cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{{Name: "BadName_", Image: "y"}},
			}},
			wantContains: []string{"RFC 1123"},
		},
		{
			name: "rejects numeric toolbox name",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent:     cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{{Name: "0", Image: "y"}},
			}},
			wantContains: []string{"must not be purely numeric"},
		},
		{
			name: "static toolbox requires both staticIP and staticVMID",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent:     cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{{Name: "tb", Mode: cocoonv1.ToolboxModeStatic}},
			}},
			wantContains: []string{"staticIP", "staticVMID"},
		},
		{
			name: "static toolbox accepts both hints",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{
					{Name: "tb", Mode: cocoonv1.ToolboxModeStatic, StaticIP: "1.2.3.4", StaticVMID: "qemu-1"},
				},
			}},
		},
		{
			name: "non-static toolbox requires image",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent:     cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{{Name: "tb", Mode: cocoonv1.ToolboxModeRun}},
			}},
			wantContains: []string{"image is required"},
		},
		{
			name: "rejects bad snapshot policy",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent:          cocoonv1.AgentSpec{Image: "x"},
				SnapshotPolicy: "every-tuesday",
			}},
			wantContains: []string{"snapshotPolicy"},
		},
		{
			name: "rejects bad hibernate policy",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent:           cocoonv1.AgentSpec{Image: "x"},
				HibernatePolicy: "relase",
			}},
			wantContains: []string{"hibernatePolicy"},
		},
		{
			name: "accepts resource quantity for storage",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x", VMOptions: cocoonv1.VMOptions{Storage: &storage100Gi}},
			}},
		},
		{
			name: "accepts firecracker + OCI + run",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					Mode:      cocoonv1.AgentModeRun,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker, OS: cocoonv1.OSLinux},
				},
			}},
		},
		{
			name: "rejects firecracker + explicit clone",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					Mode:      cocoonv1.AgentModeClone,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
				},
			}},
			wantContains: []string{"firecracker does not support clone mode"},
		},
		{
			name: "rejects firecracker + default-clone",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
				},
			}},
			wantContains: []string{"firecracker does not support clone mode"},
		},
		{
			name: "rejects firecracker toolbox in clone mode",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					Mode:      cocoonv1.AgentModeRun,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
				},
				Toolboxes: []cocoonv1.ToolboxSpec{{
					Name:      "aux",
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					Mode:      cocoonv1.ToolboxModeClone,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
				}},
			}},
			wantContains: []string{"firecracker does not support clone mode"},
		},
		{
			name: "rejects firecracker + Windows",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "ghcr.io/cocoonstack/cocoon/win:11",
					Mode:      cocoonv1.AgentModeRun,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker, OS: cocoonv1.OSWindows},
				},
			}},
			wantContains: []string{"firecracker does not support Windows"},
		},
		{
			name: "rejects firecracker + cloudimg URL",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "https://cloud-images.ubuntu.com/releases/jammy/release/ubuntu-22.04-server-cloudimg-amd64.img",
					Mode:      cocoonv1.AgentModeRun,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
				},
			}},
			wantContains: []string{"cloudimg URLs are not supported"},
		},
		{
			name: "rejects unknown backend",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x", VMOptions: cocoonv1.VMOptions{Backend: "qemu"}},
			}},
			wantContains: []string{"backend must be cloud-hypervisor or firecracker"},
		},
		{
			name: "rejects unknown connType",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x", VMOptions: cocoonv1.VMOptions{ConnType: "telnet"}},
			}},
			wantContains: []string{"connType must be ssh"},
		},
		{
			name: "rejects toolbox backend mismatch",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					Mode:      cocoonv1.AgentModeRun,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
				},
				Toolboxes: []cocoonv1.ToolboxSpec{{
					Name:      "aux",
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendCloudHypervisor},
				}},
			}},
			wantContains: []string{`backend "cloud-hypervisor" must match spec.agent.backend "firecracker"`},
		},
		{
			name: "static toolbox skips backend check",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{
					Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
					Mode:      cocoonv1.AgentModeRun,
					VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
				},
				Toolboxes: []cocoonv1.ToolboxSpec{{
					Name:       "static-box",
					Mode:       cocoonv1.ToolboxModeStatic,
					StaticIP:   "10.1.2.3",
					StaticVMID: "vm-aaa",
				}},
			}},
		},
		{
			name: "static toolbox still validates connType",
			cs: &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
				Agent: cocoonv1.AgentSpec{Image: "x"},
				Toolboxes: []cocoonv1.ToolboxSpec{{
					Name:       "static-box",
					Mode:       cocoonv1.ToolboxModeStatic,
					StaticIP:   "10.1.2.3",
					StaticVMID: "vm-aaa",
					VMOptions:  cocoonv1.VMOptions{ConnType: "telnet"},
				}},
			}},
			wantContains: []string{"connType must be ssh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateCocoonSetSpec(tt.cs)
			if len(tt.wantContains) == 0 {
				if len(errs) != 0 {
					t.Errorf("want no errors, got %v", errs)
				}
				return
			}
			for _, want := range tt.wantContains {
				if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, want) }) {
					t.Errorf("missing error containing %q, got %v", want, errs)
				}
			}
		})
	}
}

func TestValidateCocoonSetSpecReportsToolboxConnTypeOnce(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{{
			Name:      "tb",
			Image:     "y",
			VMOptions: cocoonv1.VMOptions{ConnType: "telnet"},
		}},
	}}
	errs := validateCocoonSetSpec(cs)
	var connTypeErrs []string
	for _, e := range errs {
		if strings.Contains(e, "connType must be ssh") {
			connTypeErrs = append(connTypeErrs, e)
		}
	}
	if len(connTypeErrs) != 1 {
		t.Errorf("want exactly one connType error, got %d: %v", len(connTypeErrs), errs)
	}
}

func TestValidateCocoonSetNameBudget(t *testing.T) {
	tests := []struct {
		name          string
		namespaceSize int
		setSize       int
		replicas      int32
		toolboxSize   int
		toolboxMode   cocoonv1.ToolboxMode
		os            cocoonv1.OSType
		wantField     string
		wantMax       int
	}{
		{name: "main snapshot fits exactly", namespaceSize: 7, setSize: 33},
		{name: "main snapshot exceeds by one", namespaceSize: 7, setSize: 34, wantField: "spec.agent", wantMax: 46},
		{name: "long namespace fits exactly", namespaceSize: 24, setSize: 16},
		{name: "long namespace exceeds by one", namespaceSize: 24, setSize: 17, wantField: "spec.agent", wantMax: 46},
		{name: "slot nine fits", namespaceSize: 7, setSize: 33, replicas: 9},
		{name: "slot ten exceeds", namespaceSize: 7, setSize: 33, replicas: 10, wantField: "spec.agent", wantMax: 46},
		{name: "macos main has the whole engine limit", namespaceSize: 7, setSize: 50, os: cocoonv1.OSMacos},
		{name: "macos main exceeds the engine limit", namespaceSize: 7, setSize: 51, os: cocoonv1.OSMacos, wantField: "spec.agent", wantMax: 63},
		{name: "toolbox snapshot fits exactly", namespaceSize: 7, setSize: 4, toolboxSize: 30},
		{name: "toolbox snapshot exceeds by one", namespaceSize: 7, setSize: 4, toolboxSize: 31, wantField: "spec.toolboxes[0]", wantMax: 46},
		{name: "clone toolbox snapshot exceeds", namespaceSize: 7, setSize: 4, toolboxSize: 31, toolboxMode: cocoonv1.ToolboxModeClone, wantField: "spec.toolboxes[0]", wantMax: 46},
		{name: "macos toolbox has the whole engine limit", namespaceSize: 7, setSize: 4, toolboxSize: 47, os: cocoonv1.OSMacos},
		{name: "macos toolbox exceeds the engine limit", namespaceSize: 7, setSize: 4, toolboxSize: 48, os: cocoonv1.OSMacos, wantField: "spec.toolboxes[0]", wantMax: 63},
		{name: "static toolbox has no managed snapshot", namespaceSize: 7, setSize: 4, toolboxSize: 63, toolboxMode: cocoonv1.ToolboxModeStatic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &cocoonv1.CocoonSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: strings.Repeat("n", tt.namespaceSize), Name: strings.Repeat("s", tt.setSize)},
				Spec:       cocoonv1.CocoonSetSpec{Agent: cocoonv1.AgentSpec{Image: "ubuntu:v1", Replicas: tt.replicas, VMOptions: cocoonv1.VMOptions{OS: tt.os}}},
			}
			if tt.toolboxSize > 0 {
				cs.Spec.Toolboxes = []cocoonv1.ToolboxSpec{{
					Name: strings.Repeat("t", tt.toolboxSize), Image: "tools:v1", Mode: tt.toolboxMode,
					StaticIP: "192.0.2.1", StaticVMID: "external-vm", VMOptions: cocoonv1.VMOptions{OS: tt.os},
				}}
			}
			review := buildUpdateReview(t, "CocoonSet", nil, cs)
			review.Request.Kind.Group = cocoonv1.GroupVersion.Group
			review.Request.Operation = admissionv1.Create
			review.Request.Namespace, review.Request.Name = cs.Namespace, cs.Name
			resp := newTestServer(t).validateCocoonSet(t.Context(), review)
			if tt.wantField == "" {
				if !resp.Allowed {
					t.Fatalf("valid name budget denied: %v", resp.Result)
				}
				return
			}
			if resp.Allowed || resp.Result == nil || !strings.Contains(resp.Result.Message, tt.wantField+" derives VM name") || !strings.Contains(resp.Result.Message, fmt.Sprintf("maximum is %d", tt.wantMax)) {
				t.Errorf("want name-budget denial for %s, got %+v", tt.wantField, resp)
			}
		})
	}
}

func TestValidateCocoonSetNameBudgetOnUpdate(t *testing.T) {
	tests := []struct {
		name        string
		setSize     int
		oldReplicas int32
		newReplicas int32
		wantAllowed bool
	}{
		{name: "scaling within one digit", setSize: 33, oldReplicas: 0, newReplicas: 9, wantAllowed: true},
		{name: "scaling to slot ten", setSize: 33, oldReplicas: 9, newReplicas: 10},
		{name: "repairing slot budget", setSize: 33, oldReplicas: 10, newReplicas: 9, wantAllowed: true},
		{name: "legacy finalizer removal", setSize: 34, wantAllowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := &cocoonv1.CocoonSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: strings.Repeat("s", tt.setSize), Finalizers: []string{"cocoonstack.io/cleanup"}},
				Spec:       cocoonv1.CocoonSetSpec{Agent: cocoonv1.AgentSpec{Image: "ubuntu:v1", Replicas: tt.oldReplicas}},
			}
			updated := old.DeepCopy()
			updated.Finalizers = nil
			updated.Spec.Agent.Replicas = tt.newReplicas
			review := buildUpdateReview(t, "CocoonSet", old, updated)
			review.Request.Kind.Group = cocoonv1.GroupVersion.Group
			review.Request.Namespace, review.Request.Name = updated.Namespace, updated.Name
			resp := newTestServer(t).validateCocoonSet(t.Context(), review)
			if resp.Allowed != tt.wantAllowed {
				t.Fatalf("allowed = %v, want %v: %v", resp.Allowed, tt.wantAllowed, resp.Result)
			}
			if !resp.Allowed && (resp.Result == nil || !strings.Contains(resp.Result.Message, "spec.agent derives VM name")) {
				t.Errorf("want agent name-budget denial, got %v", resp.Result)
			}
		})
	}
}

func TestValidateCocoonSetRejectsOversizedToolboxModeChange(t *testing.T) {
	old := &cocoonv1.CocoonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "demo"},
		Spec: cocoonv1.CocoonSetSpec{
			Agent: cocoonv1.AgentSpec{Image: "ubuntu:v1"},
			Toolboxes: []cocoonv1.ToolboxSpec{{
				Name: strings.Repeat("t", 31), Mode: cocoonv1.ToolboxModeStatic,
				StaticIP: "192.0.2.1", StaticVMID: "external-vm",
			}},
		},
	}
	updated := old.DeepCopy()
	updated.Spec.Toolboxes[0].Mode = cocoonv1.ToolboxModeRun
	updated.Spec.Toolboxes[0].Image = "tools:v1"
	review := buildUpdateReview(t, "CocoonSet", old, updated)
	review.Request.Kind.Group = cocoonv1.GroupVersion.Group
	review.Request.Namespace, review.Request.Name = updated.Namespace, updated.Name
	resp := newTestServer(t).validateCocoonSet(t.Context(), review)
	if resp.Allowed || resp.Result == nil || !strings.Contains(resp.Result.Message, "spec.toolboxes[0] derives VM name") {
		t.Errorf("want toolbox name-budget denial, got %+v", resp)
	}
}

func TestValidateCocoonSetRejectsVMNameCollisions(t *testing.T) {
	tests := []struct {
		name     string
		existing *cocoonv1.CocoonSet
		old      *cocoonv1.CocoonSet
		incoming *cocoonv1.CocoonSet
		wantDeny string
	}{
		{
			name:     "cross-namespace agent names",
			existing: cocoonSet("team-a", "dev", 0),
			incoming: cocoonSet("team", "a-dev", 0),
			wantDeny: `"vk-team-a-dev-0" collides with CocoonSet team-a/dev`,
		},
		{
			name:     "agent slot against a toolbox in the same namespace",
			existing: cocoonSet("a", "b-c", 2),
			incoming: withToolbox(cocoonSet("a", "b", 0), "c-2"),
			wantDeny: `"vk-a-b-c-2" collides with CocoonSet a/b-c`,
		},
		{
			name:     "distinct names",
			existing: cocoonSet("team-a", "dev", 0),
			incoming: cocoonSet("team-b", "dev", 0),
		},
		{
			name:     "update of the same CocoonSet",
			existing: cocoonSet("team-a", "dev", 0),
			old:      cocoonSet("team-a", "dev", 0),
			incoming: cocoonSet("team-a", "dev", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := newTestServer(t, tt.existing).validateCocoonSet(t.Context(), cocoonSetReview(t, tt.old, tt.incoming))
			if tt.wantDeny == "" {
				if !resp.Allowed {
					t.Fatalf("distinct VM names denied: %v", resp.Result)
				}
				return
			}
			if resp.Allowed || resp.Result == nil || !strings.Contains(resp.Result.Message, tt.wantDeny) {
				t.Errorf("want denial containing %q, got %+v", tt.wantDeny, resp)
			}
		})
	}
}

func TestValidateCocoonSetFailsClosedWhenListingCocoonSetsFails(t *testing.T) {
	srv := newTestServer(t)
	srv.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "cocoonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver unavailable")
	})
	resp := srv.validateCocoonSet(t.Context(), cocoonSetReview(t, nil, cocoonSet("team-a", "dev", 0)))
	if resp.Allowed || resp.Result == nil || !strings.Contains(resp.Result.Message, "cannot verify VM name uniqueness") {
		t.Errorf("list error should fail closed, got %+v", resp)
	}
}

func cocoonSet(namespace, name string, replicas int32) *cocoonv1.CocoonSet {
	return &cocoonv1.CocoonSet{
		TypeMeta:   metav1.TypeMeta{APIVersion: cocoonv1.GroupVersion.String(), Kind: "CocoonSet"},
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       cocoonv1.CocoonSetSpec{Agent: cocoonv1.AgentSpec{Image: "ubuntu:v1", Replicas: replicas}},
	}
}

func withToolbox(cs *cocoonv1.CocoonSet, name string) *cocoonv1.CocoonSet {
	cs.Spec.Toolboxes = append(cs.Spec.Toolboxes, cocoonv1.ToolboxSpec{Name: name, Image: "tools:v1"})
	return cs
}

func cocoonSetReview(t *testing.T, old, cs *cocoonv1.CocoonSet) *admissionv1.AdmissionReview {
	t.Helper()
	review := buildUpdateReview(t, "CocoonSet", old, cs)
	review.Request.Kind.Group = cocoonv1.GroupVersion.Group
	review.Request.Namespace, review.Request.Name = cs.Namespace, cs.Name
	if old == nil {
		review.Request.Operation = admissionv1.Create
		review.Request.OldObject.Raw = nil
	}
	return review
}
