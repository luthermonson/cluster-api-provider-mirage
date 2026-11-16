package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MachineAddress is a fake address reported for a MirageMachine.
type MachineAddress struct {
	// type is one of Hostname, ExternalIP, InternalIP, ExternalDNS, InternalDNS.
	Type string `json:"type"`
	// address is the value.
	Address string `json:"address"`
}

// MirageMachineSpec defines the desired state of a fake machine ("VM").
type MirageMachineSpec struct {
	// providerID is set by the controller once the fake VM is provisioned. The
	// Cluster API Machine controller copies it onto the owning Machine and uses
	// it to match the fake Node.
	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// failureDomain is where this machine was (fake) placed.
	// +optional
	FailureDomain string `json:"failureDomain,omitempty"`

	// nodeCPU, nodeMemory and nodePods set the capacity/allocatable the fake
	// Node advertises. Defaults are applied when empty. These allow the real
	// kube-scheduler to bind pending pods to Mirage nodes (autoscaler demo).
	// +optional
	NodeCPU string `json:"nodeCPU,omitempty"`
	// +optional
	NodeMemory string `json:"nodeMemory,omitempty"`
	// +optional
	NodePods string `json:"nodePods,omitempty"`

	// nodeLabels are extra labels placed on the fake Node (e.g. to satisfy a
	// workload's nodeSelector).
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// schedulable, when true, omits the Mirage NoSchedule taint so real pods
	// can be scheduled onto the fake Node. Worker machines used by the
	// autoscaler demo keep the default (tainted) and the demo pods tolerate it.
	// +optional
	Schedulable bool `json:"schedulable,omitempty"`
}

// MirageMachineStatus defines the observed state of a MirageMachine.
type MirageMachineStatus struct {
	// initialization reports the Cluster API contract provisioning signal.
	// +optional
	Initialization *MirageInitializationStatus `json:"initialization,omitempty"`

	// ready mirrors initialization.provisioned.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is the fake lifecycle phase: Pending, Provisioning, Provisioned,
	// Deleting.
	// +optional
	Phase string `json:"phase,omitempty"`

	// addresses are the fake machine addresses.
	// +optional
	Addresses []MachineAddress `json:"addresses,omitempty"`

	// nodeName is the fake Node registered for this machine.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// provisioningStartedAt records when the fake VM-create timer started.
	// +optional
	ProvisioningStartedAt *metav1.Time `json:"provisioningStartedAt,omitempty"`

	// targetProvisioningSeconds is the randomized VM-create duration.
	// +optional
	TargetProvisioningSeconds *int64 `json:"targetProvisioningSeconds,omitempty"`

	// nodeRegisterStartedAt records when the fake Node-registration timer
	// started (after the fake VM is provisioned, before the Node appears).
	// +optional
	NodeRegisterStartedAt *metav1.Time `json:"nodeRegisterStartedAt,omitempty"`

	// targetNodeSeconds is the randomized Node-registration duration.
	// +optional
	TargetNodeSeconds *int64 `json:"targetNodeSeconds,omitempty"`

	// deletionStartedAt records when the fake deprovision timer started.
	// +optional
	DeletionStartedAt *metav1.Time `json:"deletionStartedAt,omitempty"`

	// conditions is the standard Cluster API v1beta2 condition set.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=miragemachines,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.initialization.provisioned`
// +kubebuilder:printcolumn:name="ProviderID",type=string,JSONPath=`.spec.providerID`
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.status.nodeName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MirageMachine is a fake Cluster API infrastructure machine (a simulated VM).
type MirageMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MirageMachineSpec   `json:"spec,omitempty"`
	Status MirageMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MirageMachineList contains a list of MirageMachine.
type MirageMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageMachine `json:"items"`
}

// MirageMachineTemplateSpec defines a MirageMachine template.
type MirageMachineTemplateSpec struct {
	Template MirageMachineTemplateResource `json:"template"`
}

// MirageMachineTemplateResource is the templated MirageMachine.
type MirageMachineTemplateResource struct {
	// +optional
	ObjectMeta metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec       MirageMachineSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=miragemachinetemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:metadata:labels="cluster.x-k8s.io/v1beta2=v1alpha1"

// MirageMachineTemplate is a template for MirageMachines.
type MirageMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MirageMachineTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// MirageMachineTemplateList contains a list of MirageMachineTemplate.
type MirageMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MirageMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&MirageMachine{}, &MirageMachineList{},
		&MirageMachineTemplate{}, &MirageMachineTemplateList{},
	)
}
