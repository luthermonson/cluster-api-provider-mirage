package controller

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Phase strings shared across Mirage objects.
const (
	PhasePending      = "Pending"
	PhaseProvisioning = "Provisioning"
	PhaseProvisioned  = "Provisioned"
	PhaseDeleting     = "Deleting"
)

// isPaused reports whether reconciliation should pause for an object, honoring
// Cluster.spec.paused and the cluster.x-k8s.io/paused annotation.
func isPaused(cluster *clusterv1.Cluster, obj client.Object) bool {
	if cluster != nil && ptr.Deref(cluster.Spec.Paused, false) {
		return true
	}
	if _, ok := obj.GetAnnotations()[clusterv1.PausedAnnotation]; ok {
		return true
	}
	return false
}

// setCondition sets a metav1.Condition on a condition slice (contract-compliant:
// only type/status/reason/message/lastTransitionTime/observedGeneration are read
// by the Cluster API core controllers).
func setCondition(conds *[]metav1.Condition, gen int64, condType string, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(conds, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: gen,
	})
}

func boolPtr(b bool) *bool    { return &b }
func int64Ptr(i int64) *int64 { return &i }
