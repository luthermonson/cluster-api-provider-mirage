package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	bootstrapv1 "github.com/luthermonson/cluster-api-provider-mirage/api/bootstrap/v1alpha1"
	controlplanev1 "github.com/luthermonson/cluster-api-provider-mirage/api/controlplane/v1alpha1"
	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
	"github.com/luthermonson/cluster-api-provider-mirage/internal/mirage"
)

const infraGroup = "infrastructure.cluster.x-k8s.io"
const bootstrapGroup = "bootstrap.cluster.x-k8s.io"

// MirageControlPlaneReconciler reconciles the fake control plane: it publishes a
// kubeconfig (pointing back at the management cluster so NodeRefs resolve) and
// creates/scales control-plane Machines (+ their fake infra and bootstrap).
type MirageControlPlaneReconciler struct {
	client.Client
	Profile mirage.Profile
	// Workload builds the kubeconfig that the Cluster API core controllers use
	// to reach the (fake) workload cluster. May be nil when running out of
	// cluster, in which case kubeconfig publishing is skipped.
	Workload *mirage.WorkloadConnection
}

// +kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=miragecontrolplanes;miragecontrolplanes/status;miragecontrolplanes/scale,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;machines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=miragemachines;miragemachinetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bootstrap.cluster.x-k8s.io,resources=mirageconfigs,verbs=get;list;watch;create;update;patch;delete

func (r *MirageControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cp := &controlplanev1.MirageControlPlane{}
	if err := r.Get(ctx, req.NamespacedName, cp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, cp.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		logger.V(1).Info("waiting for Cluster owner to be set")
		return ctrl.Result{}, nil
	}

	helper, err := patch.NewHelper(cp, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if e := helper.Patch(ctx, cp); e != nil && err == nil {
			err = e
		}
	}()

	if isPaused(cluster, cp) {
		return ctrl.Result{}, nil
	}

	cp.Status.Selector = fmt.Sprintf("%s=%s,%s", clusterv1.ClusterNameLabel, cluster.Name, clusterv1.MachineControlPlaneLabel)
	cp.Status.Version = cp.Spec.Version

	if !cp.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, cp)
	}

	if !controllerutil.ContainsFinalizer(cp, infrav1.MirageFinalizer) {
		controllerutil.AddFinalizer(cp, infrav1.MirageFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	// Contract gating: control plane waits for cluster infrastructure.
	if !ptr.Deref(cluster.Status.Initialization.InfrastructureProvisioned, false) {
		logger.V(1).Info("waiting for cluster infrastructure to be provisioned")
		return ctrl.Result{}, nil
	}

	// Publish the kubeconfig secret so the core controllers can resolve NodeRefs.
	if err := r.reconcileKubeconfig(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	return r.reconcileMachines(ctx, cp, cluster)
}

func (r *MirageControlPlaneReconciler) reconcileKubeconfig(ctx context.Context, cluster *clusterv1.Cluster) error {
	if r.Workload == nil {
		return nil
	}
	name := cluster.Name + "-kubeconfig"
	existing := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, existing)
	if err == nil {
		return nil // already published; token is durable
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	data, err := r.Workload.Kubeconfig(cluster.Name)
	if err != nil {
		return fmt.Errorf("rendering kubeconfig: %w", err)
	}
	secret := kubeconfig.GenerateSecret(cluster, data)
	if err := r.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating kubeconfig secret: %w", err)
	}
	log.FromContext(ctx).Info("published fake kubeconfig", "secret", name)
	return nil
}

func (r *MirageControlPlaneReconciler) ownedMachines(ctx context.Context, cp *controlplanev1.MirageControlPlane, cluster *clusterv1.Cluster) ([]clusterv1.Machine, error) {
	var list clusterv1.MachineList
	if err := r.List(ctx, &list, client.InNamespace(cp.Namespace), client.MatchingLabels{
		clusterv1.ClusterNameLabel:         cluster.Name,
		clusterv1.MachineControlPlaneLabel: "",
	}); err != nil {
		return nil, err
	}
	out := make([]clusterv1.Machine, 0, len(list.Items))
	for i := range list.Items {
		for _, o := range list.Items[i].OwnerReferences {
			if o.UID == cp.UID {
				out = append(out, list.Items[i])
				break
			}
		}
	}
	return out, nil
}

func (r *MirageControlPlaneReconciler) reconcileMachines(ctx context.Context, cp *controlplanev1.MirageControlPlane, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	desired := ptr.Deref(cp.Spec.Replicas, 1)

	machines, err := r.ownedMachines(ctx, cp, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Scale up: create one machine per reconcile so they start on a staggered
	// timeline, then requeue quickly.
	if int32(len(machines)) < desired {
		if err := r.createMachine(ctx, cp, cluster); err != nil {
			return ctrl.Result{}, err
		}
		r.updateStatus(cp, machines, desired)
		return ctrl.Result{RequeueAfter: r.Profile.PollInterval}, nil
	}

	// Scale down: delete the most-recently-created extras.
	if int32(len(machines)) > desired {
		victim := newest(machines)
		if victim != nil {
			logger.Info("scaling down control plane", "machine", victim.Name)
			if err := r.Delete(ctx, victim); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		r.updateStatus(cp, machines, desired)
		return ctrl.Result{RequeueAfter: r.Profile.PollInterval}, nil
	}

	r.updateStatus(cp, machines, desired)

	// Requeue while not all replicas are ready so status tracks the timeline.
	if cp.Status.ReadyReplicas < desired {
		return ctrl.Result{RequeueAfter: r.Profile.PollInterval}, nil
	}
	return ctrl.Result{}, nil
}

func (r *MirageControlPlaneReconciler) updateStatus(cp *controlplanev1.MirageControlPlane, machines []clusterv1.Machine, desired int32) {
	ready := int32(0)
	for i := range machines {
		if machines[i].Status.NodeRef.IsDefined() {
			ready++
		}
	}
	cp.Status.Replicas = int32(len(machines))
	cp.Status.ReadyReplicas = ready
	cp.Status.AvailableReplicas = ready
	cp.Status.UpToDateReplicas = int32(len(machines))

	initialized := ready >= 1
	cp.Status.Initialization = &controlplanev1.MirageControlPlaneInitializationStatus{
		ControlPlaneInitialized: boolPtr(initialized),
	}
	cp.Status.Ready = desired > 0 && ready >= desired

	switch {
	case cp.Status.Ready:
		setCondition(&cp.Status.Conditions, cp.Generation, "Available", metav1.ConditionTrue, "Ready",
			"fake control plane is ready")
	case initialized:
		setCondition(&cp.Status.Conditions, cp.Generation, "Available", metav1.ConditionFalse, "ScalingUp",
			fmt.Sprintf("control plane initialized; %d/%d machines ready", ready, desired))
	default:
		setCondition(&cp.Status.Conditions, cp.Generation, "Available", metav1.ConditionFalse, "Initializing",
			"fake control plane is initializing")
	}
}

func (r *MirageControlPlaneReconciler) createMachine(ctx context.Context, cp *controlplanev1.MirageControlPlane, cluster *clusterv1.Cluster) error {
	logger := log.FromContext(ctx)

	// Resolve the MirageMachineTemplate referenced by the control plane.
	tmpl := &infrav1.MirageMachineTemplate{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cp.Namespace,
		Name:      cp.Spec.MachineTemplate.InfrastructureRef.Name,
	}, tmpl); err != nil {
		return fmt.Errorf("getting control plane MirageMachineTemplate: %w", err)
	}

	base := fmt.Sprintf("%s-%s", cp.Name, rand.String(5))
	ns := cp.Namespace

	cpOwner := *metav1.NewControllerRef(cp, controlplanev1.GroupVersion.WithKind("MirageControlPlane"))

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      base,
			Namespace: ns,
			Labels: map[string]string{
				clusterv1.ClusterNameLabel:         cluster.Name,
				clusterv1.MachineControlPlaneLabel: "",
			},
			OwnerReferences: []metav1.OwnerReference{cpOwner},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: cluster.Name,
			Version:     cp.Spec.Version,
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: bootstrapGroup, Kind: "MirageConfig", Name: base,
				},
			},
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: infraGroup, Kind: "MirageMachine", Name: base,
			},
		},
	}
	if err := r.Create(ctx, machine); err != nil {
		return fmt.Errorf("creating control plane Machine: %w", err)
	}

	machineOwner := *metav1.NewControllerRef(machine, clusterv1.GroupVersion.WithKind("Machine"))

	cfg := &bootstrapv1.MirageConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            base,
			Namespace:       ns,
			Labels:          map[string]string{clusterv1.ClusterNameLabel: cluster.Name},
			OwnerReferences: []metav1.OwnerReference{machineOwner},
		},
		Spec: bootstrapv1.MirageConfigSpec{ExtraData: "role=control-plane"},
	}
	if err := r.Create(ctx, cfg); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating control plane MirageConfig: %w", err)
	}

	mm := &infrav1.MirageMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:            base,
			Namespace:       ns,
			Labels:          map[string]string{clusterv1.ClusterNameLabel: cluster.Name},
			OwnerReferences: []metav1.OwnerReference{machineOwner},
		},
		Spec: tmpl.Spec.Template.Spec,
	}
	if err := r.Create(ctx, mm); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating control plane MirageMachine: %w", err)
	}

	logger.Info("created control plane machine", "name", base)
	return nil
}

func (r *MirageControlPlaneReconciler) reconcileDelete(ctx context.Context, cp *controlplanev1.MirageControlPlane) (ctrl.Result, error) {
	cluster, _ := util.GetOwnerCluster(ctx, r.Client, cp.ObjectMeta)
	if cluster != nil {
		machines, err := r.ownedMachines(ctx, cp, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(machines) > 0 {
			for i := range machines {
				if machines[i].DeletionTimestamp.IsZero() {
					_ = r.Delete(ctx, &machines[i])
				}
			}
			return ctrl.Result{RequeueAfter: r.Profile.PollInterval}, nil
		}
	}
	controllerutil.RemoveFinalizer(cp, infrav1.MirageFinalizer)
	return ctrl.Result{}, nil
}

func newest(machines []clusterv1.Machine) *clusterv1.Machine {
	if len(machines) == 0 {
		return nil
	}
	idx := 0
	for i := range machines {
		if machines[i].CreationTimestamp.After(machines[idx].CreationTimestamp.Time) {
			idx = i
		}
	}
	return &machines[idx]
}

func (r *MirageControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&controlplanev1.MirageControlPlane{}).
		Owns(&clusterv1.Machine{}).
		Watches(&clusterv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(r.clusterToControlPlane)).
		Complete(r)
}

func (r *MirageControlPlaneReconciler) clusterToControlPlane(ctx context.Context, o client.Object) []ctrl.Request {
	cluster, ok := o.(*clusterv1.Cluster)
	if !ok || cluster.Spec.ControlPlaneRef.Kind != "MirageControlPlane" || cluster.Spec.ControlPlaneRef.Name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.ControlPlaneRef.Name,
	}}}
}
