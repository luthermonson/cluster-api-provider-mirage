package mirage

import (
	"fmt"
	"os"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// WorkloadConnection describes how the fake workload cluster's API server is
// reached. Because Mirage registers its fake Nodes directly into the management
// cluster, the "workload" API server IS the management cluster: the generated
// kubeconfig points right back at it so the Cluster API core controllers (and a
// cluster-autoscaler in clusterapi mode) resolve NodeRefs and pending pods
// against the very Nodes Mirage creates.
type WorkloadConnection struct {
	// Server is the https URL of the management API server, as reachable from
	// inside the cluster, e.g. https://10.96.0.1:443.
	Server string
	// CAData is the API server CA bundle.
	CAData []byte
	// Token is a bearer token with permission to read Nodes/Pods (and more).
	Token []byte
}

// InClusterWorkloadConnection builds a WorkloadConnection from the pod's mounted
// service-account credentials, optionally overriding the token with a durable
// (non-expiring) token read elsewhere.
func InClusterWorkloadConnection(tokenOverride []byte) (*WorkloadConnection, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in-cluster: KUBERNETES_SERVICE_HOST/PORT unset")
	}
	ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading in-cluster CA: %w", err)
	}
	token := tokenOverride
	if len(token) == 0 {
		token, err = os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
		if err != nil {
			return nil, fmt.Errorf("reading in-cluster token: %w", err)
		}
	}
	return &WorkloadConnection{
		Server: fmt.Sprintf("https://%s:%s", host, port),
		CAData: ca,
		Token:  token,
	}, nil
}

// Kubeconfig renders a kubeconfig that authenticates to the workload (==
// management) API server with a bearer token.
func (w *WorkloadConnection) Kubeconfig(clusterName string) ([]byte, error) {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   w.Server,
		CertificateAuthorityData: w.CAData,
	}
	userName := clusterName + "-admin"
	cfg.AuthInfos[userName] = &clientcmdapi.AuthInfo{
		Token: string(w.Token),
	}
	ctxName := clusterName + "@mirage"
	cfg.Contexts[ctxName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: userName,
	}
	cfg.CurrentContext = ctxName
	return clientcmd.Write(*cfg)
}
