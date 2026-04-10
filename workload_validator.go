package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/projecteru2/core/log"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/cocoonstack/cocoon-common/meta"
)

// validateWorkload is the admission entry point for Deployment and
// StatefulSet UPDATE. It rejects scale-down on cocoon workloads
// because the agents are stateful VMs — reducing replicas would
// destroy state. Operators should use a CocoonHibernation CR to
// suspend an individual agent instead.
func (s *Server) validateWorkload(ctx context.Context, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := review.Request
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
	logger := log.WithFunc("validateDeploymentScale")

	var oldDeploy, newDeploy appsv1.Deployment
	if err := json.Unmarshal(req.OldObject.Raw, &oldDeploy); err != nil {
		logger.Warnf(ctx, "decode old deployment %s/%s: %v", req.Namespace, req.Name, err)
		return allowResponse()
	}
	if err := json.Unmarshal(req.Object.Raw, &newDeploy); err != nil {
		logger.Warnf(ctx, "decode new deployment %s/%s: %v", req.Namespace, req.Name, err)
		return allowResponse()
	}

	if !meta.HasCocoonToleration(newDeploy.Spec.Template.Spec.Tolerations) {
		return allowResponse()
	}

	return checkScaleDown(ctx, req, "Deployment",
		replicaCount(oldDeploy.Spec.Replicas),
		replicaCount(newDeploy.Spec.Replicas))
}

func validateStatefulSetScale(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	logger := log.WithFunc("validateStatefulSetScale")

	var oldSTS, newSTS appsv1.StatefulSet
	if err := json.Unmarshal(req.OldObject.Raw, &oldSTS); err != nil {
		logger.Warnf(ctx, "decode old statefulset %s/%s: %v", req.Namespace, req.Name, err)
		return allowResponse()
	}
	if err := json.Unmarshal(req.Object.Raw, &newSTS); err != nil {
		logger.Warnf(ctx, "decode new statefulset %s/%s: %v", req.Namespace, req.Name, err)
		return allowResponse()
	}

	if !meta.HasCocoonToleration(newSTS.Spec.Template.Spec.Tolerations) {
		return allowResponse()
	}

	return checkScaleDown(ctx, req, "StatefulSet",
		replicaCount(oldSTS.Spec.Replicas),
		replicaCount(newSTS.Spec.Replicas))
}

// checkScaleDown denies the request if newReplicas < oldReplicas.
func checkScaleDown(ctx context.Context, req *admissionv1.AdmissionRequest, kind string, oldReplicas, newReplicas int32) *admissionv1.AdmissionResponse {
	if newReplicas >= oldReplicas {
		recordAdmission(HandlerValidate, DecisionAllow)
		return allowResponse()
	}
	msg := fmt.Sprintf(
		"cocoon-webhook: scale-down blocked for cocoon %s %s/%s (%d -> %d). "+
			"Use a CocoonHibernation CR to suspend individual agents.",
		kind, req.Namespace, req.Name, oldReplicas, newReplicas)
	log.WithFunc("checkScaleDown").Warn(ctx, msg)
	recordAdmission(HandlerValidate, DecisionDeny)
	return denyResponse(msg)
}

// replicaCount unwraps a *int32 replica count, defaulting to 1 the
// way the apps controllers do.
func replicaCount(p *int32) int32 {
	if p != nil {
		return *p
	}
	return 1
}
