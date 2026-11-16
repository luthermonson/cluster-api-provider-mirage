//go:build e2e

// Package e2e drives the full faked Cluster API lifecycle against a live cluster
// that already has CAPI core and the Mirage provider deployed (see the README /
// `make kind-up capi-init dev`). It asserts exactly the timelines litecone's
// oracle cares about: create-to-Ready, scale up/down, and MHC remediation.
//
//	go test ./test/e2e/... -tags e2e -v -timeout 20m
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bootstrapv1 "github.com/luthermonson/cluster-api-provider-mirage/api/bootstrap/v1alpha1"
	controlplanev1 "github.com/luthermonson/cluster-api-provider-mirage/api/controlplane/v1alpha1"
	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
)

const (
	ns          = "default"
	clusterName = "mirage-e2e"
	cpName      = "mirage-e2e-cp"
	mdName      = "mirage-e2e-md-0"
	pool        = "e2e-md-0"
	k8sVersion  = "v1.34.0"
)

func newClient(t *testing.T) client.Client {
	t.Helper()
	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: buildScheme()})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return c
}

func infraRef(kind, name string) clusterv1.ContractVersionedObjectReference {
	return clusterv1.ContractVersionedObjectReference{APIGroup: "infrastructure.cluster.x-k8s.io", Kind: kind, Name: name}
}
func bootstrapRef(kind, name string) clusterv1.ContractVersionedObjectReference {
	return clusterv1.ContractVersionedObjectReference{APIGroup: "bootstrap.cluster.x-k8s.io", Kind: kind, Name: name}
}
func cpRef(kind, name string) clusterv1.ContractVersionedObjectReference {
	return clusterv1.ContractVersionedObjectReference{APIGroup: "controlplane.cluster.x-k8s.io", Kind: kind, Name: name}
}

func TestMirageLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	t.Cleanup(func() { cleanup(context.Background(), c) })
	cleanup(ctx, c) // start clean

	createCluster(t, ctx, c)

	// 1) Create -> Ready.
	t.Log("waiting for cluster to reach Provisioned with 1 CP + 3 workers Running...")
	waitUntil(t, 8*time.Minute, func() (bool, string) {
		cl := &clusterv1.Cluster{}
		if err := c.Get(ctx, key(clusterName), cl); err != nil {
			return false, err.Error()
		}
		phase := cl.Status.Phase
		cpReady := ptr.Deref(cl.Status.Initialization.ControlPlaneInitialized, false)
		running, total, withNode := workerStats(ctx, c)
		msg := fmt.Sprintf("phase=%s cpInit=%v workers running=%d/%d withNodeRef=%d", phase, cpReady, running, total, withNode)
		return phase == "Provisioned" && running == 3 && withNode == 3, msg
	})
	t.Log("create-to-Ready: PASS")

	// 2) Scale up 3 -> 5.
	scaleMD(t, ctx, c, 5)
	waitUntil(t, 5*time.Minute, func() (bool, string) {
		running, total, _ := workerStats(ctx, c)
		return running == 5 && total == 5, fmt.Sprintf("scale-up running=%d/%d", running, total)
	})
	t.Log("scale-up: PASS")

	// 3) Scale down 5 -> 2.
	scaleMD(t, ctx, c, 2)
	waitUntil(t, 5*time.Minute, func() (bool, string) {
		running, total, _ := workerStats(ctx, c)
		return running == 2 && total == 2, fmt.Sprintf("scale-down running=%d/%d", running, total)
	})
	t.Log("scale-down: PASS")

	// 4) MHC remediation: fault one worker node, assert that Machine is replaced.
	applyMHC(t, ctx, c)
	faulted := faultOneWorker(t, ctx, c)
	t.Logf("faulted machine %s; waiting for remediation (delete + replace)...", faulted)
	waitUntil(t, 6*time.Minute, func() (bool, string) {
		gone := true
		m := &clusterv1.Machine{}
		if err := c.Get(ctx, key(faulted), m); err == nil {
			gone = false
		} else if !apierrors.IsNotFound(err) {
			return false, err.Error()
		}
		running, total, _ := workerStats(ctx, c)
		return gone && running == 2 && total == 2, fmt.Sprintf("faultedGone=%v running=%d/%d", gone, running, total)
	})
	t.Log("MHC remediation: PASS")
}

func createCluster(t *testing.T, ctx context.Context, c client.Client) {
	t.Helper()
	objs := []client.Object{
		&infrav1.MirageCluster{ObjectMeta: om(clusterName)},
		&infrav1.MirageMachineTemplate{
			ObjectMeta: om(cpName),
			Spec: infrav1.MirageMachineTemplateSpec{Template: infrav1.MirageMachineTemplateResource{
				Spec: infrav1.MirageMachineSpec{NodeCPU: "4", NodeMemory: "8Gi", NodePods: "110"}}},
		},
		&controlplanev1.MirageControlPlane{
			ObjectMeta: om(cpName),
			Spec: controlplanev1.MirageControlPlaneSpec{
				Replicas: ptr.To[int32](1),
				Version:  k8sVersion,
				MachineTemplate: controlplanev1.MirageControlPlaneMachineTemplate{
					InfrastructureRef: infraRef("MirageMachineTemplate", cpName)},
			},
		},
		&clusterv1.Cluster{
			ObjectMeta: om(clusterName),
			Spec: clusterv1.ClusterSpec{
				ControlPlaneRef:   cpRef("MirageControlPlane", cpName),
				InfrastructureRef: infraRef("MirageCluster", clusterName),
			},
		},
		&infrav1.MirageMachineTemplate{
			ObjectMeta: om(mdName),
			Spec: infrav1.MirageMachineTemplateSpec{Template: infrav1.MirageMachineTemplateResource{
				Spec: infrav1.MirageMachineSpec{NodeCPU: "4", NodeMemory: "8Gi", NodePods: "110"}}},
		},
		&bootstrapv1.MirageConfigTemplate{
			ObjectMeta: om(mdName),
			Spec: bootstrapv1.MirageConfigTemplateSpec{Template: bootstrapv1.MirageConfigTemplateResource{
				Spec: bootstrapv1.MirageConfigSpec{ExtraData: "role=worker"}}},
		},
		machineDeployment(3),
	}
	for _, o := range objs {
		if err := c.Create(ctx, o); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
	}
}

func machineDeployment(replicas int32) *clusterv1.MachineDeployment {
	return &clusterv1.MachineDeployment{
		ObjectMeta: om(mdName),
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: clusterName,
			Replicas:    ptr.To(replicas),
			Selector:    metav1.LabelSelector{MatchLabels: map[string]string{"pool": pool}},
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{Labels: map[string]string{
					"pool": pool, clusterv1.ClusterNameLabel: clusterName}},
				Spec: clusterv1.MachineSpec{
					ClusterName:       clusterName,
					Version:           k8sVersion,
					Bootstrap:         clusterv1.Bootstrap{ConfigRef: bootstrapRef("MirageConfigTemplate", mdName)},
					InfrastructureRef: infraRef("MirageMachineTemplate", mdName),
				},
			},
		},
	}
}

func scaleMD(t *testing.T, ctx context.Context, c client.Client, replicas int32) {
	t.Helper()
	md := &clusterv1.MachineDeployment{}
	if err := c.Get(ctx, key(mdName), md); err != nil {
		t.Fatalf("get md: %v", err)
	}
	md.Spec.Replicas = ptr.To(replicas)
	if err := c.Update(ctx, md); err != nil {
		t.Fatalf("scale md: %v", err)
	}
	t.Logf("scaled MachineDeployment to %d", replicas)
}

func applyMHC(t *testing.T, ctx context.Context, c client.Client) {
	t.Helper()
	mhc := &clusterv1.MachineHealthCheck{
		ObjectMeta: om(mdName),
		Spec: clusterv1.MachineHealthCheckSpec{
			ClusterName: clusterName,
			Selector:    metav1.LabelSelector{MatchLabels: map[string]string{"pool": pool}},
			Checks: clusterv1.MachineHealthCheckChecks{
				NodeStartupTimeoutSeconds: ptr.To[int32](120),
				UnhealthyNodeConditions: []clusterv1.UnhealthyNodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionFalse, TimeoutSeconds: ptr.To[int32](20)},
					{Type: corev1.NodeReady, Status: corev1.ConditionUnknown, TimeoutSeconds: ptr.To[int32](20)},
				},
			},
		},
	}
	if err := c.Create(ctx, mhc); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create mhc: %v", err)
	}
}

func faultOneWorker(t *testing.T, ctx context.Context, c client.Client) string {
	t.Helper()
	machines := &clusterv1.MachineList{}
	if err := c.List(ctx, machines, client.InNamespace(ns), client.MatchingLabels{"pool": pool}); err != nil {
		t.Fatalf("list machines: %v", err)
	}
	for i := range machines.Items {
		m := &machines.Items[i]
		if !m.Status.NodeRef.IsDefined() {
			continue
		}
		node := &corev1.Node{}
		if err := c.Get(ctx, types.NamespacedName{Name: m.Status.NodeRef.Name}, node); err != nil {
			continue
		}
		if node.Annotations == nil {
			node.Annotations = map[string]string{}
		}
		node.Annotations[infrav1.UnhealthyAnnotation] = "true"
		if err := c.Update(ctx, node); err != nil {
			t.Fatalf("annotate node unhealthy: %v", err)
		}
		return m.Name
	}
	t.Fatal("no worker with a NodeRef to fault")
	return ""
}

func workerStats(ctx context.Context, c client.Client) (running, total, withNodeRef int) {
	machines := &clusterv1.MachineList{}
	if err := c.List(ctx, machines, client.InNamespace(ns), client.MatchingLabels{"pool": pool}); err != nil {
		return 0, 0, 0
	}
	for i := range machines.Items {
		m := &machines.Items[i]
		if !m.DeletionTimestamp.IsZero() {
			continue
		}
		total++
		if m.Status.Phase == string(clusterv1.MachinePhaseRunning) {
			running++
		}
		if m.Status.NodeRef.IsDefined() {
			withNodeRef++
		}
	}
	return
}

func cleanup(ctx context.Context, c client.Client) {
	_ = c.Delete(ctx, &clusterv1.MachineHealthCheck{ObjectMeta: om(mdName)})
	_ = c.Delete(ctx, &clusterv1.Cluster{ObjectMeta: om(clusterName)})
	// best-effort wait for the Cluster to disappear so a re-run starts clean
	for i := 0; i < 60; i++ {
		cl := &clusterv1.Cluster{}
		if err := c.Get(ctx, key(clusterName), cl); apierrors.IsNotFound(err) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	for _, o := range []client.Object{
		&clusterv1.MachineDeployment{ObjectMeta: om(mdName)},
		&bootstrapv1.MirageConfigTemplate{ObjectMeta: om(mdName)},
		&infrav1.MirageMachineTemplate{ObjectMeta: om(mdName)},
		&infrav1.MirageMachineTemplate{ObjectMeta: om(cpName)},
	} {
		_ = c.Delete(ctx, o)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		ok, msg := cond()
		if msg != last {
			t.Log("  " + msg)
			last = msg
		}
		if ok {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timed out after %s; last state: %s", timeout, last)
}

func om(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name, Namespace: ns} }
func key(name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}
