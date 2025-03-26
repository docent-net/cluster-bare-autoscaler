package kubeclient

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"os"
)

func Get() (*kubernetes.Clientset, error) {
	var cfg *rest.Config
	var err error

	if _, err = os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}
