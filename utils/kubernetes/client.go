package kubernetes

import (
	"net/url"

	"github.com/docent-net/cluster-bare-autoscaler/config"

	kube_client "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const (
	failedToBuildConfigErr         = "Failed to build config"
	failedToParseK8sUrlErr         = "Failed to parse Kubernetes url"
	failedToBuildClientConfigErr   = "Failed to build Kubernetes client configuration"
	failedToFindInClusterConfigErr = "Failed to find in-cluster config"
)

// CreateKubeClient creates kube client based on AutoscalingOptions.KubeClientOptions
func CreateKubeClient(opts config.KubeClientOptions) kube_client.Interface {
	return kube_client.NewForConfigOrDie(GetKubeConfig(opts))
}

// GetKubeConfig returns the rest config from AutoscalingOptions.KubeClientOptions.
func GetKubeConfig(opts config.KubeClientOptions) *rest.Config {
	var kubeConfig *rest.Config
	var err error

	if opts.KubeConfigPath != "" {
		klog.V(1).Infof("Using kubeconfig file: %s", opts.KubeConfigPath)
		// use the current context in kubeconfig
		kubeConfig, err = clientcmd.BuildConfigFromFlags("", opts.KubeConfigPath)
		if err != nil {
			klog.Fatalf("%v: %v", failedToBuildConfigErr, err)
		}
	} else if opts.Master != "" {
		url, err := url.Parse(opts.Master)
		if err != nil {
			klog.Fatalf("%v: %v", failedToParseK8sUrlErr, err)
		}

		kubeConfig, err = config.GetKubeClientConfig(url)
		if err != nil {
			klog.Fatalf("%v: %v", failedToBuildClientConfigErr, err)
		}
	} else {
		kubeConfig, err = rest.InClusterConfig()
		if err != nil {
			klog.Fatalf("%v: %v", failedToFindInClusterConfigErr, err)
		}
	}
	kubeConfig.QPS = opts.KubeClientQPS
	kubeConfig.Burst = opts.KubeClientBurst
	kubeConfig.ContentType = opts.APIContentType

	return kubeConfig
}