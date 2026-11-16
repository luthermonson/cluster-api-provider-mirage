package controller

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
	"github.com/luthermonson/cluster-api-provider-mirage/internal/mirage"
)

// MirageMachineReconciler reconciles the fake infrastructure machine (a "VM")
// and registers a fake Node for it so the Cluster API core controller can set
// the Machine's NodeRef.
type MirageMachineReconciler struct {
	client.Client
	Profile mirage.Profile
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=miragemachines;miragemachines/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *MirageMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	mm := &infrav1.MirageMachine{}
	if err := r.Get(ctx, req.NamespacedName, mm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	machine, err := util.GetOwnerMachine(ctx, r.Client, mm.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		logger.V(1).Info("waiting for Machine owner to be set")
		return ctrl.Result{}, nil
	}
	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		logger.V(1).Info("waiting for cluster-name label / Cluster")
		return ctrl.Result{}, nil
	}

	helper, err := patch.NewHelper(mm, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if e := helper.Patch(ctx, mm); e != nil && err == nil {
			err = e
		}
	}()

	if isPaused(cluster, mm) {
		return ctrl.Result{}, nil
	}

	if !mm.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mm)
	}

	if !controllerutil.ContainsFinalizer(mm, infrav1.MirageFinalizer) {
		controllerutil.AddFinalizer(mm, infrav1.MirageFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileNormal(ctx, mm, machine, cluster)
}

func (r *MirageMachineReconciler) reconcileNormal(ctx context.Context, mm *infrav1.MirageMachine, machine *clusterv1.Machine, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Contract gating: wait for cluster infra + bootstrap data secret.
	if !ptr.Deref(cluster.Status.Initialization.InfrastructureProvisioned, false) {
		logger.V(1).Info("waiting for cluster infrastructure to be provisioned")
		if mm.Status.Phase == "" {
			mm.Status.Phase = PhasePending
		}
		return ctrl.Result{}, nil
	}
	if machine.Spec.Bootstrap.DataSecretName == nil {
		logger.V(1).Info("waiting for bootstrap data secret")
		if mm.Status.Phase == "" {
			mm.Status.Phase = PhasePending
		}
		return ctrl.Result{}, nil
	}

	// Stage 2 complete: node already registered -> terminal.
	if mm.Status.NodeName != "" {
		return ctrl.Result{}, nil
	}

	// Stage 1: fake VM provisioning.
	provisioned := mm.Status.Initialization != nil && ptr.Deref(mm.Status.Initialization.Provisioned, false)
	if !provisioned {
		if mm.Status.ProvisioningStartedAt == nil {
			now := metav1.Now()
			mm.Status.ProvisioningStartedAt = &now
			mm.Status.TargetProvisioningSeconds = int64Ptr(r.Profile.TargetSeconds(mm, mirage.PhaseMachineProvision))
			mm.Status.Phase = PhaseProvisioning
			setCondition(&mm.Status.Conditions, mm.Generation, "Ready", metav1.ConditionFalse, "Provisioning",
				fmt.Sprintf("fake VM is being created (~%ds)", *mm.Status.TargetProvisioningSeconds))
			logger.Info("started fake VM creation", "targetSeconds", *mm.Status.TargetProvisioningSeconds)
			_, after := r.Profile.Elapsed(mm.Status.ProvisioningStartedAt, *mm.Status.TargetProvisioningSeconds)
			return ctrl.Result{RequeueAfter: after}, nil
		}
		done, after := r.Profile.Elapsed(mm.Status.ProvisioningStartedAt, ptr.Deref(mm.Status.TargetProvisioningSeconds, 1))
		if !done {
			mm.Status.Phase = PhaseProvisioning
			return ctrl.Result{RequeueAfter: after}, nil
		}
		// VM is "created": assign providerID and mark infra provisioned.
		if mm.Spec.ProviderID == "" {
			mm.Spec.ProviderID = fmt.Sprintf("mirage://%s/%s", mm.Namespace, mm.Name)
		}
		if mm.Spec.FailureDomain == "" {
			mm.Spec.FailureDomain = machine.Spec.FailureDomain
		}
		mm.Status.Addresses = []infrav1.MachineAddress{
			{Type: "InternalIP", Address: "10.255.0.1"},
			{Type: "Hostname", Address: mm.Name},
		}
		mm.Status.Initialization = &infrav1.MirageInitializationStatus{Provisioned: boolPtr(true)}
		mm.Status.Ready = true
		mm.Status.Phase = PhaseProvisioned
		now := metav1.Now()
		mm.Status.NodeRegisterStartedAt = &now
		mm.Status.TargetNodeSeconds = int64Ptr(r.Profile.TargetSeconds(mm, mirage.PhaseNodeRegister))
		setCondition(&mm.Status.Conditions, mm.Generation, "Ready", metav1.ConditionTrue, "Provisioned",
			"fake VM created; registering node")
		logger.Info("fake VM created", "providerID", mm.Spec.ProviderID, "nodeRegisterSeconds", *mm.Status.TargetNodeSeconds)
		_, after = r.Profile.Elapsed(mm.Status.NodeRegisterStartedAt, *mm.Status.TargetNodeSeconds)
		return ctrl.Result{RequeueAfter: after}, nil
	}

	// Stage 2: fake Node registration (kubelet boot + join).
	done, after := r.Profile.Elapsed(mm.Status.NodeRegisterStartedAt, ptr.Deref(mm.Status.TargetNodeSeconds, 1))
	if !done {
		return ctrl.Result{RequeueAfter: after}, nil
	}

	_, isControlPlane := machine.Labels[clusterv1.MachineControlPlaneLabel]
	nodeName := mm.Name
	nodeVersion := machine.Spec.Version
	if nodeVersion == "" {
		nodeVersion = "v1.34.0"
	}
	spec := mirage.NodeSpec{
		Name:         nodeName,
		ProviderID:   mm.Spec.ProviderID,
		ClusterName:  cluster.Name,
		MachineName:  machine.Name,
		Version:      nodeVersion,
		CPU:          mm.Spec.NodeCPU,
		Memory:       mm.Spec.NodeMemory,
		Pods:         mm.Spec.NodePods,
		Labels:       mm.Spec.NodeLabels,
		Schedulable:  mm.Spec.Schedulable,
		ControlPlane: isControlPlane,
	}
	if err := mirage.EnsureFakeNode(ctx, r.Client, spec); err != nil {
		return ctrl.Result{}, err
	}
	// --- fault injection: opt-in orphan "ghost" Node leak ---
	// mirage is a fake provider for exercising controllers and the tools that
	// test them. MIRAGE_GHOST_NODE_RATE=N (percent in [1,100], unset/0 disables)
	// makes a Node registration occasionally also provision a second, unowned
	// "ghost" Node — a random, reproducible-in-aggregate infrastructure leak.
	// It exists to verify that a controller-test oracle catches a genuinely
	// non-deterministic flake: the one-Node-per-Machine invariant is violated
	// only on the runs where it fires. Disabled by default, so normal runs
	// always register exactly one Node per Machine.
	if rate := ghostNodeFaultRate(); rate > 0 && rand.Intn(100) < rate {
		ghost := spec
		ghost.Name = nodeName + "-ghost"
		ghost.ProviderID = fmt.Sprintf("mirage://%s/%s-ghost", mm.Namespace, mm.Name)
		if gerr := mirage.EnsureFakeNode(ctx, r.Client, ghost); gerr != nil {
			logger.Error(gerr, "ghost-node fault: ghost Node creation failed", "ghostNode", ghost.Name)
		} else {
			logger.Info("ghost-node fault: leaked an orphan ghost Node", "ghostNode", ghost.Name, "machine", mm.Name)
		}
	}
	// --- end fault injection ---
	mm.Status.NodeName = nodeName
	setCondition(&mm.Status.Conditions, mm.Generation, "Ready", metav1.ConditionTrue, "NodeRegistered",
		fmt.Sprintf("fake node %q registered", nodeName))
	logger.Info("fake node registered", "node", nodeName)
	return ctrl.Result{}, nil
}

// ghostNodeFaultRate returns the opt-in percent chance [0,100] that registering
// a Node also leaks an orphan "ghost" Node. Set MIRAGE_GHOST_NODE_RATE to enable;
// unset/0 disables it (the default), so normal runs never leak.
func ghostNodeFaultRate() int {
	v, _ := strconv.Atoi(os.Getenv("MIRAGE_GHOST_NODE_RATE"))
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}

func (r *MirageMachineReconciler) reconcileDelete(ctx context.Context, mm *infrav1.MirageMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if mm.Status.DeletionStartedAt == nil {
		now := metav1.Now()
		mm.Status.DeletionStartedAt = &now
		mm.Status.TargetProvisioningSeconds = int64Ptr(r.Profile.TargetSeconds(mm, mirage.PhaseDelete))
		mm.Status.Phase = PhaseDeleting
		logger.Info("started fake VM deprovision", "targetSeconds", *mm.Status.TargetProvisioningSeconds)
		_, after := r.Profile.Elapsed(mm.Status.DeletionStartedAt, *mm.Status.TargetProvisioningSeconds)
		return ctrl.Result{RequeueAfter: after}, nil
	}
	done, after := r.Profile.Elapsed(mm.Status.DeletionStartedAt, ptr.Deref(mm.Status.TargetProvisioningSeconds, 1))
	if !done {
		mm.Status.Phase = PhaseDeleting
		return ctrl.Result{RequeueAfter: after}, nil
	}
	if mm.Status.NodeName != "" {
		if err := mirage.DeleteFakeNode(ctx, r.Client, mm.Status.NodeName); err != nil {
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(mm, infrav1.MirageFinalizer)
	logger.Info("fake VM deprovisioned", "node", mm.Status.NodeName)
	return ctrl.Result{}, nil
}

func (r *MirageMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.MirageMachine{}).
		Watches(&clusterv1.Machine{}, handler.EnqueueRequestsFromMapFunc(r.machineToMirageMachine)).
		Complete(r)
}

func (r *MirageMachineReconciler) machineToMirageMachine(ctx context.Context, o client.Object) []ctrl.Request {
	machine, ok := o.(*clusterv1.Machine)
	if !ok || machine.Spec.InfrastructureRef.Kind != "MirageMachine" || machine.Spec.InfrastructureRef.Name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{
		Namespace: machine.Namespace,
		Name:      machine.Spec.InfrastructureRef.Name,
	}}}
}
