package controller

import (
	"context"
	"fmt"

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

// MirageClusterReconciler reconciles the fake infrastructure cluster.
type MirageClusterReconciler struct {
	client.Client
	Profile mirage.Profile
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=mirageclusters;mirageclusters/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch

func (r *MirageClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	mc := &infrav1.MirageCluster{}
	if err := r.Get(ctx, req.NamespacedName, mc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, mc.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		logger.V(1).Info("waiting for Cluster owner to be set by the Cluster API core controller")
		return ctrl.Result{}, nil
	}

	helper, err := patch.NewHelper(mc, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if e := helper.Patch(ctx, mc); e != nil && err == nil {
			err = e
		}
	}()

	if isPaused(cluster, mc) {
		logger.V(1).Info("reconciliation is paused")
		return ctrl.Result{}, nil
	}

	if !mc.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mc)
	}

	if !controllerutil.ContainsFinalizer(mc, infrav1.MirageFinalizer) {
		controllerutil.AddFinalizer(mc, infrav1.MirageFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileNormal(ctx, mc)
}

func (r *MirageClusterReconciler) reconcileNormal(ctx context.Context, mc *infrav1.MirageCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if mc.Status.Initialization != nil && ptr.Deref(mc.Status.Initialization.Provisioned, false) {
		return ctrl.Result{}, nil // already provisioned; terminal
	}

	// Start the fake provisioning timer on first observation.
	if mc.Status.ProvisioningStartedAt == nil {
		now := metav1.Now()
		mc.Status.ProvisioningStartedAt = &now
		mc.Status.TargetProvisioningSeconds = int64Ptr(r.Profile.TargetSeconds(mc, mirage.PhaseClusterProvision))
		mc.Status.Phase = PhaseProvisioning
		setCondition(&mc.Status.Conditions, mc.Generation, "Ready", metav1.ConditionFalse, "Provisioning",
			fmt.Sprintf("fake infrastructure provisioning (~%ds)", *mc.Status.TargetProvisioningSeconds))
		logger.Info("started fake cluster infrastructure provisioning", "targetSeconds", *mc.Status.TargetProvisioningSeconds)
		_, after := r.Profile.Elapsed(mc.Status.ProvisioningStartedAt, *mc.Status.TargetProvisioningSeconds)
		return ctrl.Result{RequeueAfter: after}, nil
	}

	done, after := r.Profile.Elapsed(mc.Status.ProvisioningStartedAt, ptr.Deref(mc.Status.TargetProvisioningSeconds, 1))
	if !done {
		mc.Status.Phase = PhaseProvisioning
		return ctrl.Result{RequeueAfter: after}, nil
	}

	// Timer elapsed: fabricate the control plane endpoint and mark provisioned.
	if mc.Spec.ControlPlaneEndpoint.Host == "" {
		mc.Spec.ControlPlaneEndpoint = infrav1.APIEndpoint{
			Host: fmt.Sprintf("%s.mirage.local", mc.Name),
			Port: 6443,
		}
	}
	mc.Status.FailureDomains = mc.Spec.FailureDomains
	mc.Status.Initialization = &infrav1.MirageInitializationStatus{Provisioned: boolPtr(true)}
	mc.Status.Ready = true
	mc.Status.Phase = PhaseProvisioned
	setCondition(&mc.Status.Conditions, mc.Generation, "Ready", metav1.ConditionTrue, "Provisioned",
		"fake infrastructure is ready")
	logger.Info("fake cluster infrastructure provisioned", "endpoint", mc.Spec.ControlPlaneEndpoint.Host)
	return ctrl.Result{}, nil
}

func (r *MirageClusterReconciler) reconcileDelete(ctx context.Context, mc *infrav1.MirageCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if mc.Status.DeletionStartedAt == nil {
		now := metav1.Now()
		mc.Status.DeletionStartedAt = &now
		mc.Status.TargetProvisioningSeconds = int64Ptr(r.Profile.TargetSeconds(mc, mirage.PhaseDelete))
		mc.Status.Phase = PhaseDeleting
		_, after := r.Profile.Elapsed(mc.Status.DeletionStartedAt, *mc.Status.TargetProvisioningSeconds)
		logger.Info("started fake cluster infrastructure teardown", "targetSeconds", *mc.Status.TargetProvisioningSeconds)
		return ctrl.Result{RequeueAfter: after}, nil
	}
	done, after := r.Profile.Elapsed(mc.Status.DeletionStartedAt, ptr.Deref(mc.Status.TargetProvisioningSeconds, 1))
	if !done {
		mc.Status.Phase = PhaseDeleting
		return ctrl.Result{RequeueAfter: after}, nil
	}
	controllerutil.RemoveFinalizer(mc, infrav1.MirageFinalizer)
	logger.Info("fake cluster infrastructure torn down")
	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller, watching Clusters so a MirageCluster is
// re-reconciled when its owner Cluster appears / unpauses.
func (r *MirageClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.MirageCluster{}).
		Watches(&clusterv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(r.clusterToMirageCluster)).
		Complete(r)
}

func (r *MirageClusterReconciler) clusterToMirageCluster(ctx context.Context, o client.Object) []ctrl.Request {
	cluster, ok := o.(*clusterv1.Cluster)
	if !ok {
		return nil
	}
	if cluster.Spec.InfrastructureRef.Kind != "MirageCluster" || cluster.Spec.InfrastructureRef.Name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}}}
}
