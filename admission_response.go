package main

import (
	admissionv1 "k8s.io/api/admission/v1"
)

// allowResponse returns a permissive AdmissionResponse with no patch.
// Subsequent commits add deny / patch builders alongside the handlers
// that need them, so this file stays focused on the unconditional
// "allow" path that every handler shares.
func allowResponse() *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{Allowed: true}
}
