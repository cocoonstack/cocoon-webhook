package admission

import (
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
)

func TestValidateCocoonSetSpec(t *testing.T) {
	storage100Gi := resource.MustParse("100Gi")

	tests := []struct {
		name string
		cs   *cocoonv1.CocoonSet
		// wantContains lists substrings each of which must appear in some
		// returned error. Empty means the spec must validate cleanly (zero
		// errors returned).
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

func TestSpecEqualDetectsMetadataOnlyChange(t *testing.T) {
	base := cocoonv1.CocoonSet{
		Spec: cocoonv1.CocoonSetSpec{
			Agent: cocoonv1.AgentSpec{
				Image:     "ghcr.io/x:1",
				Mode:      cocoonv1.AgentModeClone,
				VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
			},
		},
	}

	withFinalizer := base.DeepCopy()
	withFinalizer.Finalizers = []string{"cocoonset.cocoonstack.io/finalizer"}
	if !specEqual(&base, withFinalizer) {
		t.Errorf("specEqual should return true when only metadata differs")
	}

	diffSpec := base.DeepCopy()
	diffSpec.Spec.Agent.Mode = cocoonv1.AgentModeRun
	if specEqual(&base, diffSpec) {
		t.Errorf("specEqual should return false when spec differs")
	}
}
