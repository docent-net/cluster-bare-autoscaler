package nodeops_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"

	v1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestListShutdownNodeNames_OrdersOldestFirst(t *testing.T) {
	now := time.Now().UTC()
	nA := nodeWith("node-a",
		map[string]string{"cba.dev/is-managed": "true"},
		map[string]string{nodeops.AnnotationPoweredOff: now.Add(-48 * time.Hour).Format(time.RFC3339)},
	)
	nB := nodeWith("node-b",
		map[string]string{"cba.dev/is-managed": "true"},
		map[string]string{nodeops.AnnotationPoweredOff: now.Add(-24 * time.Hour).Format(time.RFC3339)},
	)
	// node-c has NO annotation, but state tracker marks it as powered-off
	nC := nodeWith("node-c",
		map[string]string{"cba.dev/is-managed": "true"},
		nil,
	)

	client := fake.NewSimpleClientset(&nA, &nB, &nC)
	tracker := nodeops.NewNodeStateTracker()
	tracker.MarkPoweredOff("node-c") // treated as "very old"

	got, err := nodeops.ListShutdownNodeNames(context.Background(), client, nodeops.ManagedNodeFilter{
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels:  map[string]string{},
	}, tracker)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	want := []string{"node-c", "node-a", "node-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order mismatch\ngot:  %v\nwant: %v", got, want)
	}
}

func TestListShutdownNodeNames_EmptyWhenNone(t *testing.T) {
	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: meta.ObjectMeta{
			Name:   "node-a",
			Labels: map[string]string{"cba.dev/is-managed": "true"},
		},
	})
	tracker := nodeops.NewNodeStateTracker()
	got, err := nodeops.ListShutdownNodeNames(context.Background(), client, nodeops.ManagedNodeFilter{
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels:  map[string]string{},
	}, tracker)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}
