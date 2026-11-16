package main

import (
	"context"
	"flag"
	"os"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	bootstrapv1 "github.com/luthermonson/cluster-api-provider-mirage/api/bootstrap/v1alpha1"
	controlplanev1 "github.com/luthermonson/cluster-api-provider-mirage/api/controlplane/v1alpha1"
	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
	"github.com/luthermonson/cluster-api-provider-mirage/internal/controller"
	"github.com/luthermonson/cluster-api-provider-mirage/internal/mirage"
)

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = coordinationv1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)
	_ = bootstrapv1.AddToScheme(scheme)
	_ = controlplanev1.AddToScheme(scheme)
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		timingProfile        string
		enableFakeKubelet    bool
		fakeKubeletInterval  time.Duration
		workloadTokenSecret  string

		clusterProvision   time.Duration
		machineProvision   time.Duration
		bootstrapProvision time.Duration
		nodeRegister       time.Duration
		deleteDuration     time.Duration
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&timingProfile, "timing-profile", envOr("MIRAGE_TIMING_PROFILE", "fast"), "Fake-timing preset: 'fast' (CI/e2e) or 'demo' (slow, lifelike).")
	flag.BoolVar(&enableFakeKubelet, "fake-kubelet", true, "Keep fake Nodes Ready and mark scheduled pods Running.")
	flag.DurationVar(&fakeKubeletInterval, "fake-kubelet-interval", 5*time.Second, "Fake kubelet heartbeat interval.")
	flag.StringVar(&workloadTokenSecret, "workload-token-secret", envOr("MIRAGE_WORKLOAD_TOKEN_SECRET", "mirage-system/mirage-workload-token"), "namespace/name of a service-account token Secret used to build workload kubeconfigs.")

	flag.DurationVar(&clusterProvision, "cluster-provision", 0, "Override base duration for fake cluster provisioning (0 = profile default).")
	flag.DurationVar(&machineProvision, "machine-provision", 0, "Override base duration for fake VM creation (0 = profile default).")
	flag.DurationVar(&bootstrapProvision, "bootstrap-provision", 0, "Override base duration for fake bootstrap (0 = profile default).")
	flag.DurationVar(&nodeRegister, "node-register", 0, "Override base duration for fake node registration (0 = profile default).")
	flag.DurationVar(&deleteDuration, "delete-duration", 0, "Override base duration for fake deprovision (0 = profile default).")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	profile := mirage.FastProfile()
	if timingProfile == "demo" {
		profile = mirage.DemoProfile()
	}
	if clusterProvision > 0 {
		profile.ClusterProvision = clusterProvision
	}
	if machineProvision > 0 {
		profile.MachineProvision = machineProvision
	}
	if bootstrapProvision > 0 {
		profile.BootstrapProvision = bootstrapProvision
	}
	if nodeRegister > 0 {
		profile.NodeRegister = nodeRegister
	}
	if deleteDuration > 0 {
		profile.Delete = deleteDuration
	}
	setupLog.Info("fake timing", "profile", timingProfile,
		"clusterProvision", profile.ClusterProvision, "machineProvision", profile.MachineProvision,
		"bootstrapProvision", profile.BootstrapProvision, "nodeRegister", profile.NodeRegister, "delete", profile.Delete)

	restCfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "mirage.cluster.x-k8s.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	workload := buildWorkloadConnection(restCfg, workloadTokenSecret, setupLog)

	if err := (&controller.MirageClusterReconciler{Client: mgr.GetClient(), Profile: profile}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MirageCluster")
		os.Exit(1)
	}
	if err := (&controller.MirageMachineReconciler{Client: mgr.GetClient(), Profile: profile}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MirageMachine")
		os.Exit(1)
	}
	if err := (&controller.MirageConfigReconciler{Client: mgr.GetClient(), Profile: profile}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MirageConfig")
		os.Exit(1)
	}
	if err := (&controller.MirageControlPlaneReconciler{Client: mgr.GetClient(), Profile: profile, Workload: workload}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MirageControlPlane")
		os.Exit(1)
	}

	if enableFakeKubelet {
		if err := mgr.Add(&mirage.FakeKubelet{Client: mgr.GetClient(), Interval: fakeKubeletInterval}); err != nil {
			setupLog.Error(err, "unable to add fake kubelet")
			os.Exit(1)
		}
	}

	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// buildWorkloadConnection resolves how the fake workload cluster (== the
// management cluster) is reached, preferring a durable token Secret so the
// published kubeconfig does not expire.
func buildWorkloadConnection(cfg *rest.Config, tokenSecret string, log logrLogger) *mirage.WorkloadConnection {
	var tokenOverride []byte
	if tokenSecret != "" {
		if ns, name, ok := splitNN(tokenSecret); ok {
			c, err := client.New(cfg, client.Options{Scheme: scheme})
			if err == nil {
				s := &corev1.Secret{}
				if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, s); err == nil {
					if t, ok := s.Data["token"]; ok && len(t) > 0 {
						tokenOverride = t
						log.Info("using durable workload token", "secret", tokenSecret)
					}
				} else {
					log.Info("workload token secret not found; falling back to in-cluster token", "secret", tokenSecret, "err", err.Error())
				}
			}
		}
	}
	wc, err := mirage.InClusterWorkloadConnection(tokenOverride)
	if err != nil {
		log.Info("not running in-cluster; kubeconfig publishing disabled (NodeRefs will not resolve)", "err", err.Error())
		return nil
	}
	return wc
}

func splitNN(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// logrLogger is the minimal logger surface used above.
type logrLogger interface {
	Info(msg string, keysAndValues ...any)
}
