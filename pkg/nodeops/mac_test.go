package nodeops_test

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

func TestGetMACAddressFromNode(t *testing.T) {
	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"cba.dev/mac-address": "00:11:22:33:44:55",
			},
		},
	}

	config := nodeops.NodeAnnotationConfig{MAC: ""}
	mac := nodeops.GetMACAddressFromNode(node, config)
	if mac != "00:11:22:33:44:55" {
		t.Errorf("expected MAC, got: %s", mac)
	}
}
