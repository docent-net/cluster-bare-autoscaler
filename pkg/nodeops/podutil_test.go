package nodeops_test

import (
	"context"
	"testing"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestFindPodIPOnNode_Found(t *testing.T) {
	client := fake.NewSimpleClientset(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "cluster-services",
			Labels: map[string]string{
				"app": "poweroff-manager",
			},
		},
		Spec:   v1.PodSpec{NodeName: "node1"},
		Status: v1.PodStatus{PodIP: "10.0.0.1"},
	})

	ip, err := nodeops.FindPodIPOnNode(context.Background(), client, "cluster-services", "app=poweroff-manager", "node1")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", ip)
}

func TestFindPodIPOnNode_NoMatch(t *testing.T) {
	client := fake.NewSimpleClientset() // No pods

	_, err := nodeops.FindPodIPOnNode(context.Background(), client, "cluster-services", "app=poweroff-manager", "node1")
	require.ErrorContains(t, err, "no pod with label")
}

func TestFindPodIPOnNode_LabelParseError(t *testing.T) {
	client := fake.NewSimpleClientset()

	_, err := nodeops.FindPodIPOnNode(context.Background(), client, "cluster-services", "=", "node1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "found '='")
}
