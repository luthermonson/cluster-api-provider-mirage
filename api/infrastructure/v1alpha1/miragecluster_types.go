package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MirageClusterSpec defines the desired state of a fake infrastructure cluster.
type MirageClusterSpec struct {
	// controlPlaneEndpoint is the endpoint used to contact the (fake) control
	// plane. If left empty, the MirageCluster controller fabricates one.
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// failureDomains is the static set of fake failure domains this cluster
	// advertises. They are mirrored to status.failureDomains once provisioned.
	// +optional
	FailureDomains []FailureDomain `json:"failureDomains,omitempty"`
}

// MirageClusterStatus defines the observed state of a MirageCluster.
type MirageClusterStatus struct {
	// initialization reports the Cluster API contract provisioning signal.
	// +optional
	Initialization *MirageInitializationStatus `json:"initialization,omitempty"`

	// ready is a human-friendly mirror of initialization.provisioned.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is the fake lifecycle phase: Pending, Provisioning, Provisioned,
	// Deleting.
	// +optional
	Phase string `json:"phase,omitempty"`

	// failureDomains is the Cluster API contract list of failure domains.
	// +optional
	FailureDomains []FailureDomain `json:"failureDomains,omitempty"`

	// provisioningStartedAt records when the fake provisioning timer started.
	// +optional
	ProvisioningStartedAt *metav1.Time `json:"provisioningStartedAt,omitempty"`

	// targetProvisioningSeconds is the randomized duration chosen for this
	// object's fake provisioning. The object stays Provisioning until this
	// many seconds have elapsed since provisioningStartedAt.
	// +optional
	TargetProvisioningSeconds *int64 `json:"targetProvisioningSeconds,omitempty"`

	// deletionStartedAt records when the fake teardown timer started.
	// +optional
	DeletionStartedAt *metav1.Time `json:"deletionStartedAt,omitempty"`

	// conditions is the standard Cluster API v1beta2 condition set.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=mirageclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.initialization.provisioned`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.controlPlaneEndpoint.host`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MirageCluster is a fake Cluster API infrastructure cluster.
type MirageCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MirageClusterSpec   `json:"spec,omitempty"`
	Status MirageClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MirageClusterList contains a list of MirageCluster.
type MirageClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageCluster `json:"items"`
}

// MirageClusterTemplateSpec defines a MirageCluster template.
type MirageClusterTemplateSpec struct {
	Template MirageClusterTemplateResource `json:"template"`
}

// MirageClusterTemplateResource is the templated MirageCluster.
type MirageClusterTemplateResource struct {
	// +optional
	ObjectMeta metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec       MirageClusterSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=mirageclustertemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"

// MirageClusterTemplate is a template for MirageClusters (ClusterClass support).
type MirageClusterTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MirageClusterTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// MirageClusterTemplateList contains a list of MirageClusterTemplate.
type MirageClusterTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageClusterTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&MirageCluster{}, &MirageClusterList{},
		&MirageClusterTemplate{}, &MirageClusterTemplateList{},
	)
}
