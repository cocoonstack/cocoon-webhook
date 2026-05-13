package admission

import (
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cocoonv1 "github.com/cocoonstack/cocoon-common/apis/v1"
)

func TestValidateCocoonSetSpecAcceptsMinimal(t *testing.T) {
	cs := &cocoonv1.CocoonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "ns"},
		Spec: cocoonv1.CocoonSetSpec{
			Agent: cocoonv1.AgentSpec{
				Image: "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
			},
		},
	}
	if errs := validateCocoonSetSpec(cs); len(errs) != 0 {
		t.Errorf("minimal valid spec produced errors: %v", errs)
	}
}

func TestValidateCocoonSetSpecRejectsMissingImage(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "spec.agent.image") }) {
		t.Errorf("expected agent.image error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecRejectsNegativeReplicas(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x", Replicas: -1},
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "replicas must be >= 0") }) {
		t.Errorf("expected replicas error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecRejectsBadMode(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x", Mode: "ouija"},
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "agent.mode") }) {
		t.Errorf("expected mode error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecRejectsDuplicateToolboxNames(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{
			{Name: "tb", Image: "y"},
			{Name: "tb", Image: "z"},
		},
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "duplicates an earlier toolbox") }) {
		t.Errorf("expected duplicate toolbox error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecRejectsBadToolboxName(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{
			{Name: "BadName_", Image: "y"},
		},
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "RFC 1123") }) {
		t.Errorf("expected RFC 1123 error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecRejectsNumericToolboxName(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{
			{Name: "0", Image: "y"},
		},
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "must not be purely numeric") }) {
		t.Errorf("expected numeric name error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecStaticToolboxRequiresHints(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{
			{Name: "tb", Mode: cocoonv1.ToolboxModeStatic},
		},
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "staticIP") }) {
		t.Errorf("expected staticIP error, got %v", errs)
	}
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "staticVMID") }) {
		t.Errorf("expected staticVMID error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecStaticToolboxAcceptsBoth(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{
			{Name: "tb", Mode: cocoonv1.ToolboxModeStatic, StaticIP: "1.2.3.4", StaticVMID: "qemu-1"},
		},
	}}
	if errs := validateCocoonSetSpec(cs); len(errs) != 0 {
		t.Errorf("static toolbox with hints should be valid, got %v", errs)
	}
}

func TestValidateCocoonSetSpecNonStaticToolboxRequiresImage(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{
			{Name: "tb", Mode: cocoonv1.ToolboxModeRun},
		},
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "image is required") }) {
		t.Errorf("expected image error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecBadSnapshotPolicy(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent:          cocoonv1.AgentSpec{Image: "x"},
		SnapshotPolicy: "every-tuesday",
	}}
	errs := validateCocoonSetSpec(cs)
	if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "snapshotPolicy") }) {
		t.Errorf("expected snapshotPolicy error, got %v", errs)
	}
}

func TestValidateCocoonSetSpecAcceptsResourceQuantity(t *testing.T) {
	q := resource.MustParse("100Gi")
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x", VMOptions: cocoonv1.VMOptions{Storage: &q}},
	}}
	if errs := validateCocoonSetSpec(cs); len(errs) != 0 {
		t.Errorf("storage quantity should be valid, got %v", errs)
	}
}

func TestValidateCocoonSetSpecAcceptsFirecrackerOCIRun(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{
			Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
			Mode:      cocoonv1.AgentModeRun,
			VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker, OS: cocoonv1.OSLinux},
		},
	}}
	if errs := validateCocoonSetSpec(cs); len(errs) != 0 {
		t.Errorf("firecracker + OCI + mode=run should be valid, got %v", errs)
	}
}

func TestValidateCocoonSetSpecRejectsFirecrackerClone(t *testing.T) {
	// Explicit clone
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{
			Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
			Mode:      cocoonv1.AgentModeClone,
			VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
		},
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool { return strings.Contains(e, "firecracker does not support clone mode") }) {
		t.Errorf("expected fc+clone rejection")
	}

	// Default mode (empty → clone) should also be rejected
	csDefault := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{
			Image:     "ghcr.io/cocoonstack/cocoon/ubuntu:24.04",
			VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
		},
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(csDefault), func(e string) bool { return strings.Contains(e, "firecracker does not support clone mode") }) {
		t.Errorf("expected fc+default-clone rejection")
	}
}

func TestValidateCocoonSetSpecRejectsFirecrackerToolboxClone(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
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
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool { return strings.Contains(e, "firecracker does not support clone mode") }) {
		t.Errorf("expected fc toolbox clone rejection")
	}
}

func TestValidateCocoonSetSpecRejectsFirecrackerWindows(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{
			Image:     "ghcr.io/cocoonstack/cocoon/win:11",
			Mode:      cocoonv1.AgentModeRun,
			VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker, OS: cocoonv1.OSWindows},
		},
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool { return strings.Contains(e, "firecracker does not support Windows") }) {
		t.Errorf("expected fc+windows rejection")
	}
}

func TestValidateCocoonSetSpecRejectsFirecrackerCloudimg(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{
			Image:     "https://cloud-images.ubuntu.com/releases/jammy/release/ubuntu-22.04-server-cloudimg-amd64.img",
			Mode:      cocoonv1.AgentModeRun,
			VMOptions: cocoonv1.VMOptions{Backend: cocoonv1.BackendFirecracker},
		},
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool { return strings.Contains(e, "cloudimg URLs are not supported") }) {
		t.Errorf("expected fc+cloudimg URL rejection")
	}
}

func TestValidateCocoonSetSpecRejectsBadBackend(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x", VMOptions: cocoonv1.VMOptions{Backend: "qemu"}},
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool { return strings.Contains(e, "backend must be cloud-hypervisor or firecracker") }) {
		t.Errorf("expected backend enum rejection")
	}
}

func TestValidateCocoonSetSpecRejectsBadConnType(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x", VMOptions: cocoonv1.VMOptions{ConnType: "telnet"}},
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool { return strings.Contains(e, "connType must be ssh") }) {
		t.Errorf("expected connType enum rejection")
	}
}

func TestValidateCocoonSetSpecRejectsToolboxBackendMismatch(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
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
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool {
		return strings.Contains(e, "backend \"cloud-hypervisor\" must match spec.agent.backend \"firecracker\"")
	}) {
		t.Errorf("expected toolbox backend mismatch rejection, got %v", validateCocoonSetSpec(cs))
	}
}

func TestValidateCocoonSetSpecStaticToolboxSkipsBackend(t *testing.T) {
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
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
			// No backend / image — static toolboxes don't run a hypervisor.
		}},
	}}
	if errs := validateCocoonSetSpec(cs); len(errs) != 0 {
		t.Errorf("static toolbox should bypass backend check, got %v", errs)
	}
}

func TestValidateCocoonSetSpecStaticToolboxValidatesConnType(t *testing.T) {
	// Static toolboxes skip backend/image checks, but ConnType still applies
	// because clients reach them via SSH/RDP/VNC/ADB regardless of where the
	// VM runs.
	cs := &cocoonv1.CocoonSet{Spec: cocoonv1.CocoonSetSpec{
		Agent: cocoonv1.AgentSpec{Image: "x"},
		Toolboxes: []cocoonv1.ToolboxSpec{{
			Name:       "static-box",
			Mode:       cocoonv1.ToolboxModeStatic,
			StaticIP:   "10.1.2.3",
			StaticVMID: "vm-aaa",
			VMOptions:  cocoonv1.VMOptions{ConnType: "telnet"},
		}},
	}}
	if !slices.ContainsFunc(validateCocoonSetSpec(cs), func(e string) bool { return strings.Contains(e, "connType must be ssh") }) {
		t.Errorf("expected static toolbox connType rejection, got %v", validateCocoonSetSpec(cs))
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

	// Same spec, different metadata → specEqual=true
	withFinalizer := base.DeepCopy()
	withFinalizer.Finalizers = []string{"cocoonset.cocoonstack.io/finalizer"}
	if !specEqual(&base, withFinalizer) {
		t.Errorf("specEqual should return true when only metadata differs")
	}

	// Different spec → specEqual=false
	diffSpec := base.DeepCopy()
	diffSpec.Spec.Agent.Mode = cocoonv1.AgentModeRun
	if specEqual(&base, diffSpec) {
		t.Errorf("specEqual should return false when spec differs")
	}
}
