// Package v1alpha1 contains the Mirage FAKE control-plane provider API types.
//
// A real control-plane provider (e.g. KubeadmControlPlane) stands up etcd and
// the kube-apiserver on control plane machines and writes a kubeconfig that
// reaches them. Mirage fakes all of that: it fabricates a kubeconfig Secret that
// points back at the management cluster (so the Cluster API core controllers can
// still resolve NodeRefs against the fake Nodes), creates the requested number
// of control-plane Machines, and reports controlPlaneInitialized / ready once
// those Machines come up — on a randomized timeline.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// MirageControlPlaneMachineTemplate describes the control-plane Machines.
type MirageControlPlaneMachineTemplate struct {
	// metadata is propagated onto control-plane Machines.
	// +optional
	ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty"`

	// infrastructureRef points at the MirageMachineTemplate used to create the
	// infrastructure for each control-plane Machine.
	InfrastructureRef clusterv1.ContractVersionedObjectReference `json:"infrastructureRef"`
}

// MirageControlPlaneSpec defines the desired state of a fake control plane.
type MirageControlPlaneSpec struct {
	// replicas is the number of control-plane machines. Odd numbers (1,3,5)
	// are typical. Defaults to 1.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// version is the Kubernetes version of the control plane, e.g. v1.34.0.
	Version string `json:"version"`

	// machineTemplate describes the control-plane Machines to create.
	MachineTemplate MirageControlPlaneMachineTemplate `json:"machineTemplate"`
}

// MirageControlPlaneInitializationStatus reports the control-plane contract signal.
type MirageControlPlaneInitializationStatus struct {
	// controlPlaneInitialized is true once the fake control plane is able to
	// accept requests (i.e. the first control-plane machine is up and the
	// kubeconfig has been published).
	// +optional
	ControlPlaneInitialized *bool `json:"controlPlaneInitialized,omitempty"`
}

// MirageControlPlaneStatus defines the observed state of a MirageControlPlane.
type MirageControlPlaneStatus struct {
	// initialization reports the Cluster API control-plane contract signal.
	// +optional
	Initialization *MirageControlPlaneInitializationStatus `json:"initialization,omitempty"`

	// ready is true when all desired control-plane machines are up.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// selector is the label selector used by the scale subresource.
	// +optional
	Selector string `json:"selector,omitempty"`

	// replicas is the total number of control-plane machines.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// readyReplicas is the number of ready control-plane machines.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// availableReplicas is the number of available control-plane machines.
	// +optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`

	// upToDateReplicas is the number of up-to-date control-plane machines.
	// +optional
	UpToDateReplicas int32 `json:"upToDateReplicas,omitempty"`

	// version is the observed Kubernetes version.
	// +optional
	Version string `json:"version,omitempty"`

	// conditions is the standard Cluster API v1beta2 condition set.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
// +kubebuilder:resource:path=miragecontrolplanes,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"
// +kubebuilder:printcolumn:name="Initialized",type=string,JSONPath=`.status.initialization.controlPlaneInitialized`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="ReadyReplicas",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`

// MirageControlPlane is a fake Cluster API control plane.
type MirageControlPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MirageControlPlaneSpec   `json:"spec,omitempty"`
	Status MirageControlPlaneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MirageControlPlaneList contains a list of MirageControlPlane.
type MirageControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageControlPlane `json:"items"`
}

// MirageControlPlaneTemplateSpec defines a MirageControlPlane template.
type MirageControlPlaneTemplateSpec struct {
	Template MirageControlPlaneTemplateResource `json:"template"`
}

// MirageControlPlaneTemplateResource is the templated MirageControlPlane.
type MirageControlPlaneTemplateResource struct {
	// +optional
	ObjectMeta metav1.ObjectMeta      `json:"metadata,omitempty"`
	Spec       MirageControlPlaneSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=miragecontrolplanetemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"

// MirageControlPlaneTemplate is a template for MirageControlPlanes.
type MirageControlPlaneTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MirageControlPlaneTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// MirageControlPlaneTemplateList contains a list of MirageControlPlaneTemplate.
type MirageControlPlaneTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageControlPlaneTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&MirageControlPlane{}, &MirageControlPlaneList{},
		&MirageControlPlaneTemplate{}, &MirageControlPlaneTemplateList{},
	)
}
