package kube

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClient builds a Kubernetes clientset from the standard kubeconfig
// loading rules (KUBECONFIG env var, ~/.kube/config), with optional
// overrides for the kubeconfig path and context.
func NewClient(kubeconfig, kubeContext string) (kubernetes.Interface, string, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig: %w", err)
	}

	namespace, _, err := clientConfig.Namespace()
	if err != nil {
		namespace = "default"
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, "", fmt.Errorf("create client: %w", err)
	}
	return clientset, namespace, nil
}
