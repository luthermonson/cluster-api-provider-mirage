package mirage

import (
	"context"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
)

// NodeSpec describes the fake Node to register for a MirageMachine.
type NodeSpec struct {
	Name         string
	ProviderID   string
	ClusterName  string
	MachineName  string
	Version      string
	CPU          string
	Memory       string
	Pods         string
	Labels       map[string]string
	Schedulable  bool
	ControlPlane bool
}

func defaulted(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// EnsureFakeNode creates or updates the fake Node and its lease so that it is
// Ready, advertises real allocatable capacity (so the scheduler can bind pods
// to it) and carries the Mirage markers.
func EnsureFakeNode(ctx context.Context, c client.Client, s NodeSpec) error {
	cpu := resource.MustParse(defaulted(s.CPU, "4"))
	mem := resource.MustParse(defaulted(s.Memory, "8Gi"))
	pods := resource.MustParse(defaulted(s.Pods, "110"))
	eph := resource.MustParse("50Gi")
	capacity := corev1.ResourceList{
		corev1.ResourceCPU:              cpu,
		corev1.ResourceMemory:           mem,
		corev1.ResourcePods:             pods,
		corev1.ResourceEphemeralStorage: eph,
	}

	labels := map[string]string{
		"kubernetes.io/hostname":        s.Name,
		"kubernetes.io/os":              "linux",
		"kubernetes.io/arch":            "amd64",
		infrav1.FakeNodeLabel:           "true",
		infrav1.MachineNameLabel:        s.MachineName,
		"cluster.x-k8s.io/cluster-name": s.ClusterName,
	}
	if s.ControlPlane {
		labels["node-role.kubernetes.io/control-plane"] = ""
	}
	for k, v := range s.Labels {
		labels[k] = v
	}

	var taints []corev1.Taint
	if !s.Schedulable {
		taints = append(taints, corev1.Taint{
			Key:    infrav1.FakeNodeTaintKey,
			Value:  "true",
			Effect: corev1.TaintEffectNoSchedule,
		})
	}
	if s.ControlPlane {
		taints = append(taints, corev1.Taint{
			Key:    "node-role.kubernetes.io/control-plane",
			Effect: corev1.TaintEffectNoSchedule,
		})
	}

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: s.Name}}
	_, err := controllerutil.CreateOrPatch(ctx, c, node, func() error {
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		for k, v := range labels {
			node.Labels[k] = v
		}
		node.Spec.ProviderID = s.ProviderID
		node.Spec.Taints = taints
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensuring fake node %s: %w", s.Name, err)
	}

	// Status subresource: capacity, allocatable, Ready condition + heartbeat.
	if err := c.Get(ctx, types.NamespacedName{Name: s.Name}, node); err != nil {
		return err
	}
	now := metav1.Now()
	node.Status.Capacity = capacity
	node.Status.Allocatable = capacity.DeepCopy()
	node.Status.NodeInfo.KubeletVersion = defaulted(s.Version, "v1.34.0")
	node.Status.NodeInfo.OperatingSystem = "linux"
	node.Status.NodeInfo.Architecture = "amd64"
	node.Status.Addresses = []corev1.NodeAddress{
		{Type: corev1.NodeHostName, Address: s.Name},
		{Type: corev1.NodeInternalIP, Address: "10.255.0.1"},
	}
	node.Status.Conditions = []corev1.NodeCondition{
		cond(corev1.NodeReady, corev1.ConditionTrue, "KubeletReady", "mirage fake kubelet is posting ready", now),
		cond(corev1.NodeMemoryPressure, corev1.ConditionFalse, "KubeletHasSufficientMemory", "", now),
		cond(corev1.NodeDiskPressure, corev1.ConditionFalse, "KubeletHasNoDiskPressure", "", now),
		cond(corev1.NodePIDPressure, corev1.ConditionFalse, "KubeletHasSufficientPID", "", now),
	}
	if err := c.Status().Update(ctx, node); err != nil {
		return fmt.Errorf("updating fake node status %s: %w", s.Name, err)
	}

	return ensureLease(ctx, c, s.Name)
}

func cond(t corev1.NodeConditionType, s corev1.ConditionStatus, reason, msg string, now metav1.Time) corev1.NodeCondition {
	return corev1.NodeCondition{
		Type:               t,
		Status:             s,
		Reason:             reason,
		Message:            msg,
		LastHeartbeatTime:  now,
		LastTransitionTime: now,
	}
}

// ensureLease creates/renews the node Lease so the node-lifecycle-controller
// keeps treating the fake Node as healthy.
func ensureLease(ctx context.Context, c client.Client, nodeName string) error {
	dur := int32(40)
	now := metav1.NewMicroTime(time.Now())
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "kube-node-lease"},
	}
	_, err := controllerutil.CreateOrPatch(ctx, c, lease, func() error {
		lease.Spec.HolderIdentity = &nodeName
		lease.Spec.LeaseDurationSeconds = &dur
		lease.Spec.RenewTime = &now
		return nil
	})
	if apierrors.IsNotFound(err) {
		return nil // kube-node-lease namespace may not exist; non-fatal
	}
	return err
}

// DeleteFakeNode removes the fake Node and its lease.
func DeleteFakeNode(ctx context.Context, c client.Client, name string) error {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Delete(ctx, node); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	lease := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kube-node-lease"}}
	if err := c.Delete(ctx, lease); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) {
		return err
	}
	return nil
}

// FakeKubelet is a manager Runnable that keeps every fake Node's heartbeat/lease
// fresh and marks pods scheduled onto fake Nodes as Running (there is no real
// kubelet to do it). This closes the autoscaler loop: pending pods that the
// scheduler binds to a freshly-provisioned Mirage Node reach the Running phase.
type FakeKubelet struct {
	Client   client.Client
	Interval time.Duration
}

// Start implements manager.Runnable.
func (f *FakeKubelet) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("fake-kubelet")
	if f.Interval <= 0 {
		f.Interval = 5 * time.Second
	}
	t := time.NewTicker(f.Interval)
	defer t.Stop()
	logger.Info("fake kubelet started", "interval", f.Interval)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := f.tick(ctx); err != nil {
				logger.Error(err, "fake kubelet tick failed")
			}
		}
	}
}

func (f *FakeKubelet) tick(ctx context.Context) error {
	var nodes corev1.NodeList
	if err := f.Client.List(ctx, &nodes, client.MatchingLabels{infrav1.FakeNodeLabel: "true"}); err != nil {
		return err
	}
	fake := map[string]bool{}
	for i := range nodes.Items {
		n := &nodes.Items[i]
		fake[n.Name] = true
		now := metav1.Now()

		// Fault injection: an operator (or litecone) can force a fake Node
		// NotReady to exercise MachineHealthCheck remediation.
		_, unhealthy := n.Annotations[infrav1.UnhealthyAnnotation]
		if unhealthy {
			if setNodeReady(n, corev1.ConditionFalse, "MirageFaultInjected", "unhealthy annotation present", now) {
				_ = f.Client.Status().Update(ctx, n)
			}
			continue // stop renewing the lease so the node goes unreachable
		}

		// Healthy path: renew heartbeat + lease.
		_ = ensureLease(ctx, f.Client, n.Name)
		changed := setNodeReady(n, corev1.ConditionTrue, "KubeletReady", "mirage fake kubelet is posting ready", now)
		for j := range n.Status.Conditions {
			n.Status.Conditions[j].LastHeartbeatTime = now
			changed = true
		}
		if changed {
			_ = f.Client.Status().Update(ctx, n)
		}
	}
	if len(fake) == 0 {
		return nil
	}

	// Mark pods bound to fake nodes as Running.
	var pods corev1.PodList
	if err := f.Client.List(ctx, &pods); err != nil {
		return err
	}
	now := metav1.Now()
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName == "" || !fake[p.Spec.NodeName] {
			continue
		}
		if p.Status.Phase == corev1.PodRunning {
			continue
		}
		p.Status.Phase = corev1.PodRunning
		if p.Status.StartTime == nil {
			p.Status.StartTime = &now
		}
		if p.Status.PodIP == "" {
			p.Status.PodIP = "10.244.255.254"
			p.Status.PodIPs = []corev1.PodIP{{IP: p.Status.PodIP}}
		}
		p.Status.HostIP = "10.255.0.1"
		p.Status.Conditions = []corev1.PodCondition{
			podCond(corev1.PodScheduled, now),
			podCond(corev1.PodInitialized, now),
			podCond(corev1.ContainersReady, now),
			podCond(corev1.PodReady, now),
		}
		p.Status.ContainerStatuses = fakeContainerStatuses(p, now)
		_ = f.Client.Status().Update(ctx, p)
	}
	return nil
}

// setNodeReady updates the Node's Ready condition, returning whether it changed
// the status value (not just the heartbeat).
func setNodeReady(n *corev1.Node, status corev1.ConditionStatus, reason, msg string, now metav1.Time) bool {
	for j := range n.Status.Conditions {
		if n.Status.Conditions[j].Type == corev1.NodeReady {
			if n.Status.Conditions[j].Status != status {
				n.Status.Conditions[j].Status = status
				n.Status.Conditions[j].Reason = reason
				n.Status.Conditions[j].Message = msg
				n.Status.Conditions[j].LastTransitionTime = now
				return true
			}
			return false
		}
	}
	n.Status.Conditions = append(n.Status.Conditions, cond(corev1.NodeReady, status, reason, msg, now))
	return true
}

func podCond(t corev1.PodConditionType, now metav1.Time) corev1.PodCondition {
	return corev1.PodCondition{Type: t, Status: corev1.ConditionTrue, LastTransitionTime: now}
}

func fakeContainerStatuses(p *corev1.Pod, now metav1.Time) []corev1.ContainerStatus {
	out := make([]corev1.ContainerStatus, 0, len(p.Spec.Containers))
	for _, ctr := range p.Spec.Containers {
		out = append(out, corev1.ContainerStatus{
			Name:         ctr.Name,
			Ready:        true,
			Started:      ptrBool(true),
			Image:        ctr.Image,
			ImageID:      "mirage://" + ctr.Image,
			ContainerID:  "mirage://" + ctr.Name,
			RestartCount: 0,
			State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}},
		})
	}
	return out
}

func ptrBool(b bool) *bool { return &b }
