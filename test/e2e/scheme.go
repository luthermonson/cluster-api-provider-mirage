//go:build e2e

package e2e

import (
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	bootstrapv1 "github.com/luthermonson/cluster-api-provider-mirage/api/bootstrap/v1alpha1"
	controlplanev1 "github.com/luthermonson/cluster-api-provider-mirage/api/controlplane/v1alpha1"
	infrav1 "github.com/luthermonson/cluster-api-provider-mirage/api/infrastructure/v1alpha1"
)

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = clusterv1.AddToScheme(s)
	_ = infrav1.AddToScheme(s)
	_ = bootstrapv1.AddToScheme(s)
	_ = controlplanev1.AddToScheme(s)
	return s
}
