package nodeops_test

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

func TestListManagedNodes(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-a",
			Labels: map[string]string{
				"cba.dev/is-managed": "true",
			},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-b",
			Labels: map[string]string{
				"cba.dev/is-managed": "false",
			},
		},
	})

	nodes, err := nodeops.ListManagedNodes(context.Background(), client, nodeops.ManagedNodeFilter{
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "node-a" {
		t.Errorf("expected node-a, got: %+v", nodes)
	}
}
