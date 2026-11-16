/*
Package v1alpha1 contains the Mirage infrastructure provider API types.

Mirage is a FAKE Cluster API infrastructure provider. Nothing it reconciles
creates real infrastructure: InfraClusters, InfraMachines, Nodes and the
control plane are all simulated. Every simulated operation holds in its
in-progress phase for a randomized, configurable duration (RequeueAfter)
before flipping to its terminal state, so there is a real wall-clock timeline
to observe.
*/
package v1alpha1

// Annotation and label keys shared across all Mirage API groups.
const (
	// ProvisionDurationAnnotation overrides, per object, how long the fake
	// "provisioning" phase is held before the object flips to provisioned.
	// Value is a Go duration string, e.g. "45s" or "2m".
	ProvisionDurationAnnotation = "mirage.cluster.x-k8s.io/provision-duration"

	// ProvisionJitterAnnotation overrides, per object, the +/- jitter applied
	// to the provision duration. Value is a Go duration string.
	ProvisionJitterAnnotation = "mirage.cluster.x-k8s.io/provision-jitter"

	// DeleteDurationAnnotation overrides, per object, how long the fake
	// "deleting" phase is held before the object is actually removed.
	DeleteDurationAnnotation = "mirage.cluster.x-k8s.io/delete-duration"

	// FakeNodeLabel marks the Node objects that Mirage registers into the
	// management cluster to stand in for real Kubernetes nodes.
	FakeNodeLabel = "mirage.cluster.x-k8s.io/fake-node"

	// FakeNodeTaintKey is placed (NoSchedule) on fake worker Nodes so that
	// real system workloads are not scheduled onto nodes with no kubelet.
	// Workloads that want to land on Mirage nodes (e.g. the autoscaler demo)
	// must tolerate this taint.
	FakeNodeTaintKey = "mirage.cluster.x-k8s.io/fake"

	// MachineNameLabel records, on a fake Node, the Machine it belongs to.
	MachineNameLabel = "mirage.cluster.x-k8s.io/machine"

	// UnhealthyAnnotation, when present on a fake Node, makes the fake kubelet
	// report the Node as NotReady and stop renewing its lease. This is the hook
	// for demonstrating MachineHealthCheck remediation:
	//   kubectl annotate node <node> mirage.cluster.x-k8s.io/unhealthy=true
	UnhealthyAnnotation = "mirage.cluster.x-k8s.io/unhealthy"
)

// MirageFinalizer is added to Mirage objects so the fake deprovision delay can
// run before the object is removed.
const MirageFinalizer = "mirage.cluster.x-k8s.io/finalizer"

// APIEndpoint represents a reachable Kubernetes API endpoint.
type APIEndpoint struct {
	// host is the hostname on which the API server is serving.
	// +optional
	Host string `json:"host,omitempty"`

	// port is the port on which the API server is serving.
	// +optional
	Port int32 `json:"port,omitempty"`
}

// MirageInitializationStatus reports the Cluster API contract initialization
// signal (status.initialization.provisioned) for an infrastructure object.
type MirageInitializationStatus struct {
	// provisioned is true when the fake infrastructure has finished
	// provisioning. This is the v1beta2 Cluster API contract field.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// FailureDomain is a fake failure domain exposed by a MirageCluster.
type FailureDomain struct {
	// name is the unique name of the failure domain.
	Name string `json:"name"`

	// controlPlane is true if control plane machines may be placed here.
	// +optional
	ControlPlane *bool `json:"controlPlane,omitempty"`
}
