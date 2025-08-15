package kubeclient

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var inClusterConfig = rest.InClusterConfig

// Get creates a Kubernetes clientset from in-cluster config or falls back to kubeconfig
func Get() (*kubernetes.Clientset, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

// GetRestConfig returns a *rest.Config from in-cluster config or kubeconfig (for dev)
func GetRestConfig() (*rest.Config, error) {
	if cfg, err := inClusterConfig(); err == nil { // seam
		return cfg, nil
	}

	// Try in-cluster first
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fallback to local kubeconfig
	kubeconfig := filepath.Join(homeDir(), ".kube", "config")
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return nil, fmt.Errorf("kubeconfig not found and not running in-cluster")
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // for Windows
}
