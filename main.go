// cocoon-webhook — Admission webhook for stateful VM scheduling and protection.
//
// Mutating (/mutate — Pod CREATE):
//  1. Derives a stable VM name from the Deployment/ReplicaSet owner + replica slot
//  2. Looks up the ConfigMap "cocoon-vm-affinity" for last-known node
//  3. Sets pod.spec.nodeName + cocoon.cis/vm-name annotation
//
// Validating (/validate — Deployment/StatefulSet UPDATE):
//  4. Blocks scale-down for cocoon-type workloads (only scale-up allowed)
//     Agents are stateful VMs — reducing replicas would destroy state.
//     Use the Hibernation CRD to suspend individual agents instead.
//
// For pods without a Deployment owner (bare pods, StatefulSets), the
// webhook uses the pod name directly as the VM name.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/cocoonstack/cocoon-operator/pkg/k8sutil"
	"github.com/projecteru2/core/log"
	"github.com/projecteru2/core/types"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Constants for ConfigMap name, annotation key, and toleration key used to
// identify and schedule cocoon VM workloads.
const (
	affinityConfigMap = "cocoon-vm-affinity"
	vmNameAnnotation  = "cocoon.cis/vm-name"
	cocoonToleration  = "virtual-kubelet.io/provider"
)

var clientset *kubernetes.Clientset

// jsonPatch represents a single RFC 6902 JSON Patch operation.
type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// --- entry point ---

func main() {
	ctx := context.Background()
	logLevel := envOrDefault("WEBHOOK_LOG_LEVEL", "info")
	if err := log.SetupLog(ctx, &types.ServerLogConfig{Level: logLevel}, ""); err != nil {
		log.WithFunc("main").Fatalf(ctx, err, "setup log: %v", err)
	}

	config, err := k8sutil.LoadConfig()
	if err != nil {
		log.WithFunc("main").Fatalf(ctx, err, "k8s config: %v", err)
	}
	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.WithFunc("main").Fatalf(ctx, err, "init clientset: %v", err)
	}

	certFile := envOrDefault("TLS_CERT", "/etc/cocoon/webhook/certs/tls.crt")
	keyFile := envOrDefault("TLS_KEY", "/etc/cocoon/webhook/certs/tls.key")

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", handleMutate)
	mux.HandleFunc("/validate", handleValidate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.WithFunc("main").Fatalf(ctx, err, "load TLS: %v", err)
	}
	server := &http.Server{
		Addr:              ":8443",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.WithFunc("main").Info(ctx, "cocoon-webhook listening on :8443")
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.WithFunc("main").Fatalf(ctx, err, "listen and serve: %v", err)
		}
	}()
	<-ctx.Done()
	if err := server.Shutdown(context.Background()); err != nil {
		log.WithFunc("main").Warnf(context.Background(), "shutdown: %v", err)
	}
}

// --- HTTP handlers ---

// handleMutate processes mutating admission requests for Pod CREATE operations.
func handleMutate(w http.ResponseWriter, r *http.Request) {
	review, err := decodeAdmissionReview(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	review.Response = mutate(r.Context(), review.Request)
	review.Response.UID = review.Request.UID

	writeJSON(w, review)
}

// handleValidate processes validating admission requests for scale-down protection.
func handleValidate(w http.ResponseWriter, r *http.Request) {
	review, err := decodeAdmissionReview(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	review.Response = validate(r.Context(), review.Request)
	review.Response.UID = review.Request.UID

	writeJSON(w, review)
}

// --- mutating admission logic ---

func mutate(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	if req.Kind.Kind != "Pod" {
		return allowResponse()
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return allowResponse()
	}

	// Only mutate pods with cocoon toleration (VM workloads).
	if !hasCocoonToleration(pod.Spec.Tolerations) {
		return allowResponse()
	}

	// Skip mutation for CocoonSet-owned pods — controller handles everything.
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "CocoonSet" {
			log.WithFunc("mutate").Infof(ctx, "mutate %s/%s: owned by CocoonSet %s, skipping", req.Namespace, req.Name, ref.Name)
			return allowResponse()
		}
	}

	// Already has nodeName set — don't override.
	if pod.Spec.NodeName != "" {
		return allowResponse()
	}

	// Fetch the affinity ConfigMap once for both lookupVMNode and allocateSlot.
	cm, err := clientset.CoreV1().ConfigMaps(req.Namespace).Get(ctx, affinityConfigMap, metav1.GetOptions{})
	if err != nil {
		log.WithFunc("mutate").Warnf(ctx, "mutate %s/%s: failed to get ConfigMap %s: %v", req.Namespace, req.Name, affinityConfigMap, err)
		cm = nil
	}

	// Already has vm-name annotation — don't override.
	if pod.Annotations != nil && pod.Annotations[vmNameAnnotation] != "" {
		vmName := pod.Annotations[vmNameAnnotation]
		if nodeName := lookupVMNode(cm, vmName); nodeName != "" {
			return patchNodeName(ctx, nodeName)
		}
		// No affinity record — let scheduler pick any cocoon node.
		return pickCocoonNode(ctx)
	}

	// Derive stable VM name from owner (Deployment/RS) + namespace.
	vmName := deriveVMName(ctx, &pod, req.Namespace, req.Name, cm)

	// Look up last-known node for this VM.
	nodeName := lookupVMNode(cm, vmName)

	var patches []jsonPatch

	// Ensure annotations map exists.
	if pod.Annotations == nil {
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: map[string]string{},
		})
	}

	// Set vm-name annotation.
	patches = append(patches, jsonPatch{
		Op:    "add",
		Path:  "/metadata/annotations/" + escapeJSONPointer(vmNameAnnotation),
		Value: vmName,
	})

	// Set nodeName if we have affinity, otherwise let scheduler pick a cocoon node.
	if nodeName != "" {
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/spec/nodeName",
			Value: nodeName,
		})
		log.WithFunc("mutate").Infof(ctx, "mutate %s/%s: vm=%s -> node=%s (affinity)", req.Namespace, req.Name, vmName, nodeName)
	} else {
		// First-time: pick any cocoon-pool node.
		if node := pickAnyCocoonNode(ctx); node != "" {
			patches = append(patches, jsonPatch{
				Op:    "add",
				Path:  "/spec/nodeName",
				Value: node,
			})
			log.WithFunc("mutate").Infof(ctx, "mutate %s/%s: vm=%s -> node=%s (new, round-robin)", req.Namespace, req.Name, vmName, node)
		}
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		log.WithFunc("mutate").Error(ctx, err, "marshal patches")
		return allowResponse()
	}
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patchBytes,
		PatchType: &pt,
	}
}

// --- validating admission logic ---

// validate rejects scale-down (replicas decrease) for cocoon-type Deployments
// and StatefulSets. Agents are stateful VMs — scale-down would destroy state.
// Use the Hibernation CRD to suspend individual agents instead.
func validate(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	if req.Operation != admissionv1.Update {
		return allowResponse()
	}

	switch req.Kind.Kind {
	case "Deployment":
		return validateDeploymentScale(ctx, req)
	case "StatefulSet":
		return validateStatefulSetScale(ctx, req)
	default:
		return allowResponse()
	}
}

func validateDeploymentScale(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldDeploy, newDeploy appsv1.Deployment
	if err := json.Unmarshal(req.OldObject.Raw, &oldDeploy); err != nil {
		return allowResponse()
	}
	if err := json.Unmarshal(req.Object.Raw, &newDeploy); err != nil {
		return allowResponse()
	}

	if !hasCocoonToleration(newDeploy.Spec.Template.Spec.Tolerations) {
		return allowResponse()
	}

	return checkScaleDown(
		ctx, req, "Deployment",
		replicaCount(oldDeploy.Spec.Replicas),
		replicaCount(newDeploy.Spec.Replicas),
	)
}

func validateStatefulSetScale(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	var oldSTS, newSTS appsv1.StatefulSet
	if err := json.Unmarshal(req.OldObject.Raw, &oldSTS); err != nil {
		return allowResponse()
	}
	if err := json.Unmarshal(req.Object.Raw, &newSTS); err != nil {
		return allowResponse()
	}

	if !hasCocoonToleration(newSTS.Spec.Template.Spec.Tolerations) {
		return allowResponse()
	}

	return checkScaleDown(
		ctx, req, "StatefulSet",
		replicaCount(oldSTS.Spec.Replicas),
		replicaCount(newSTS.Spec.Replicas),
	)
}

// checkScaleDown denies the request if newReplicas < oldReplicas.
func checkScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest, kind string, oldReplicas, newReplicas int32) *admissionv1.AdmissionResponse {
	if newReplicas >= oldReplicas {
		return allowResponse()
	}
	msg := fmt.Sprintf(
		"cocoon-webhook: scale-down blocked for cocoon %s %s/%s (%d -> %d). "+
			"Use Hibernation CRD to suspend individual agents.",
		kind, req.Namespace, req.Name, oldReplicas, newReplicas)
	log.WithFunc("checkScaleDown").Infof(ctx, "validate DENY: %s", msg)
	return denyResponse(msg)
}

// --- VM name derivation and slot allocation ---

// deriveVMName creates a stable VM name from the pod's owner chain.
// For Deployment pods: vk-{ns}-{deployment-name}-{slot}
// For bare pods: vk-{ns}-{pod-name}
func deriveVMName(ctx context.Context, pod *corev1.Pod, ns, podName string, cm *corev1.ConfigMap) string {
	// Walk owner references: Pod -> ReplicaSet -> Deployment.
	deployName := ""
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			// RS name = {deployment}-{hash}, strip the hash.
			parts := strings.Split(ref.Name, "-")
			if len(parts) >= 2 {
				deployName = strings.Join(parts[:len(parts)-1], "-")
			}
		}
	}

	if deployName != "" {
		slot, err := allocateSlot(ns, deployName, podName, cm)
		if err != nil {
			log.WithFunc("deriveVMName").Warnf(ctx, "allocateSlot %s/%s: %v, skipping slot allocation", ns, deployName, err)
			return fmt.Sprintf("vk-%s-%s", ns, podName)
		}
		return fmt.Sprintf("vk-%s-%s-%d", ns, deployName, slot)
	}

	// Bare pod or StatefulSet: use pod name directly.
	return fmt.Sprintf("vk-%s-%s", ns, podName)
}

// allocateSlot assigns a stable replica index for a Deployment pod.
// Reads the affinity ConfigMap to track slot assignments.
func allocateSlot(ns, deployName, podName string, cm *corev1.ConfigMap) (int, error) {
	if cm == nil {
		return 0, fmt.Errorf("no ConfigMap available for slot allocation")
	}

	prefix := fmt.Sprintf("vk-%s-%s-", ns, deployName)
	usedSlots := map[int]string{} // slot -> podName
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

	// Check if this pod already has a slot.
	for slot, pn := range usedSlots {
		if pn == podName {
			return slot, nil
		}
	}

	// Find first empty slot (pod was deleted).
	for i := 0; i <= maxSlot; i++ {
		if _, used := usedSlots[i]; !used {
			return i, nil
		}
	}

	// All slots occupied — allocate new.
	return maxSlot + 1, nil
}

// --- node lookup and selection ---

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

func pickAnyCocoonNode(ctx context.Context) string {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}

	var cocoonNodes []string
	for _, n := range nodes.Items {
		if n.Labels["type"] == "virtual-kubelet" {
			cocoonNodes = append(cocoonNodes, n.Name)
			continue
		}
		for _, taint := range n.Spec.Taints {
			if taint.Key == cocoonToleration {
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

// --- admission response helpers ---

func allowResponse() *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{Allowed: true}
}

func denyResponse(msg string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		Result:  &metav1.Status{Message: msg, Reason: metav1.StatusReasonForbidden},
	}
}

func patchNodeName(ctx context.Context, nodeName string) *admissionv1.AdmissionResponse {
	patches := []jsonPatch{{
		Op:    "add",
		Path:  "/spec/nodeName",
		Value: nodeName,
	}}
	patchBytes, err := json.Marshal(patches)
	if err != nil {
		log.WithFunc("patchNodeName").Error(ctx, err, "marshal patches")
		return allowResponse()
	}
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patchBytes,
		PatchType: &pt,
	}
}

func pickCocoonNode(ctx context.Context) *admissionv1.AdmissionResponse {
	node := pickAnyCocoonNode(ctx)
	if node == "" {
		return allowResponse()
	}
	return patchNodeName(ctx, node)
}

// --- general helpers ---

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func decodeAdmissionReview(r *http.Request) (*admissionv1.AdmissionReview, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &review, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	out, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// hasCocoonToleration checks whether a toleration list includes the cocoon
// virtual-kubelet provider key.
func hasCocoonToleration(tolerations []corev1.Toleration) bool {
	for _, t := range tolerations {
		if t.Key == cocoonToleration {
			return true
		}
	}
	return false
}

func replicaCount(p *int32) int32 {
	if p != nil {
		return *p
	}
	return 1
}

// parseConfigMapField extracts the value for a given key from a
// comma-separated "key:value" string (e.g. "node:cocoon-pool,pod:xxx").
func parseConfigMapField(data, key string) string {
	prefix := key + ":"
	for part := range strings.SplitSeq(data, ",") {
		if val, ok := strings.CutPrefix(part, prefix); ok {
			return val
		}
	}
	return ""
}

func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}
