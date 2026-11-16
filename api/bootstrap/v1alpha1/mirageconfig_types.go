// Package v1alpha1 contains the Mirage FAKE bootstrap provider API types.
//
// A real bootstrap provider turns machine configuration into cloud-init/ignition
// that a kubelet runs at boot. Mirage fakes this: after a randomized delay it
// writes a dummy bootstrap Secret and reports dataSecretCreated=true, which is
// all the Cluster API contract requires to unblock the Machine lifecycle.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MirageConfigInitializationStatus reports the bootstrap contract signal.
type MirageConfigInitializationStatus struct {
	// dataSecretCreated is true when the fake bootstrap secret exists.
	// +optional
	DataSecretCreated *bool `json:"dataSecretCreated,omitempty"`
}

// MirageConfigSpec defines the desired state of a fake bootstrap config.
type MirageConfigSpec struct {
	// extraData is arbitrary content folded into the fake bootstrap payload,
	// purely so templates can demonstrate per-role configuration.
	// +optional
	ExtraData string `json:"extraData,omitempty"`
}

// MirageConfigStatus defines the observed state of a MirageConfig.
type MirageConfigStatus struct {
	// initialization reports the Cluster API bootstrap contract signal.
	// +optional
	Initialization *MirageConfigInitializationStatus `json:"initialization,omitempty"`

	// ready mirrors initialization.dataSecretCreated.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// dataSecretName is the name of the generated bootstrap data secret.
	// +optional
	DataSecretName *string `json:"dataSecretName,omitempty"`

	// provisioningStartedAt records when the fake bootstrap timer started.
	// +optional
	ProvisioningStartedAt *metav1.Time `json:"provisioningStartedAt,omitempty"`

	// targetProvisioningSeconds is the randomized bootstrap duration.
	// +optional
	TargetProvisioningSeconds *int64 `json:"targetProvisioningSeconds,omitempty"`

	// conditions is the standard Cluster API v1beta2 condition set.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=mirageconfigs,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.initialization.dataSecretCreated`
// +kubebuilder:printcolumn:name="DataSecret",type=string,JSONPath=`.status.dataSecretName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MirageConfig is a fake Cluster API bootstrap config.
type MirageConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MirageConfigSpec   `json:"spec,omitempty"`
	Status MirageConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MirageConfigList contains a list of MirageConfig.
type MirageConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageConfig `json:"items"`
}

// MirageConfigTemplateSpec defines a MirageConfig template.
type MirageConfigTemplateSpec struct {
	Template MirageConfigTemplateResource `json:"template"`
}

// MirageConfigTemplateResource is the templated MirageConfig.
type MirageConfigTemplateResource struct {
	// +optional
	ObjectMeta metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec       MirageConfigSpec  `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=mirageconfigtemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"

// MirageConfigTemplate is a template for MirageConfigs.
type MirageConfigTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MirageConfigTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// MirageConfigTemplateList contains a list of MirageConfigTemplate.
type MirageConfigTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageConfigTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&MirageConfig{}, &MirageConfigList{},
		&MirageConfigTemplate{}, &MirageConfigTemplateList{},
	)
}
