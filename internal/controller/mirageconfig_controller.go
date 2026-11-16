package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	bootstrapv1 "github.com/luthermonson/cluster-api-provider-mirage/api/bootstrap/v1alpha1"
	"github.com/luthermonson/cluster-api-provider-mirage/internal/mirage"
)

// MirageConfigReconciler reconciles the fake bootstrap config.
type MirageConfigReconciler struct {
	client.Client
	Profile mirage.Profile
}

// +kubebuilder:rbac:groups=bootstrap.cluster.x-k8s.io,resources=mirageconfigs;mirageconfigs/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch

func (r *MirageConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cfg := &bootstrapv1.MirageConfig{}
	if err := r.Get(ctx, req.NamespacedName, cfg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	machine, err := util.GetOwnerMachine(ctx, r.Client, cfg.ObjectMeta)
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

	helper, err := patch.NewHelper(cfg, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if e := helper.Patch(ctx, cfg); e != nil && err == nil {
			err = e
		}
	}()

	if isPaused(cluster, cfg) {
		return ctrl.Result{}, nil
	}

	if cfg.Status.Initialization != nil && ptr.Deref(cfg.Status.Initialization.DataSecretCreated, false) {
		return ctrl.Result{}, nil
	}

	// Fake bootstrap timer.
	if cfg.Status.ProvisioningStartedAt == nil {
		now := metav1.Now()
		cfg.Status.ProvisioningStartedAt = &now
		cfg.Status.TargetProvisioningSeconds = int64Ptr(r.Profile.TargetSeconds(cfg, mirage.PhaseBootstrapProvision))
		setCondition(&cfg.Status.Conditions, cfg.Generation, "Ready", metav1.ConditionFalse, "Generating",
			"fake bootstrap data is being generated")
		_, after := r.Profile.Elapsed(cfg.Status.ProvisioningStartedAt, *cfg.Status.TargetProvisioningSeconds)
		logger.Info("started fake bootstrap generation", "targetSeconds", *cfg.Status.TargetProvisioningSeconds)
		return ctrl.Result{RequeueAfter: after}, nil
	}
	done, after := r.Profile.Elapsed(cfg.Status.ProvisioningStartedAt, ptr.Deref(cfg.Status.TargetProvisioningSeconds, 1))
	if !done {
		return ctrl.Result{RequeueAfter: after}, nil
	}

	// Timer elapsed: write the fake bootstrap data secret.
	secretName := cfg.Name
	data := fmt.Sprintf("## mirage fake bootstrap for %s\n## cluster: %s\n## machine: %s\n%s\n",
		cfg.Name, cluster.Name, machine.Name, cfg.Spec.ExtraData)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cfg.Namespace,
			Labels:    map[string]string{clusterv1.ClusterNameLabel: cluster.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: bootstrapv1.GroupVersion.String(),
				Kind:       "MirageConfig",
				Name:       cfg.Name,
				UID:        cfg.UID,
				Controller: boolPtr(true),
			}},
		},
		Type: clusterv1.ClusterSecretType,
		Data: map[string][]byte{"value": []byte(data), "format": []byte("mirage")},
	}
	if err := r.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("creating bootstrap secret: %w", err)
	}

	cfg.Status.DataSecretName = ptr.To(secretName)
	cfg.Status.Initialization = &bootstrapv1.MirageConfigInitializationStatus{DataSecretCreated: boolPtr(true)}
	cfg.Status.Ready = true
	setCondition(&cfg.Status.Conditions, cfg.Generation, "Ready", metav1.ConditionTrue, "Generated",
		"fake bootstrap data secret created")
	logger.Info("fake bootstrap data secret created", "secret", secretName)
	return ctrl.Result{}, nil
}

func (r *MirageConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bootstrapv1.MirageConfig{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
