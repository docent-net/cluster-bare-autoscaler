package nodeops_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestRunOnce_AnnotatesDiscoveredMAC(t *testing.T) {
	macServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"mac": "aa:bb:cc:dd:ee:ff"})
	}))
	defer macServer.Close()
	macIP := strings.TrimPrefix(macServer.URL, "http://")

	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"cba.dev/is-managed": "true",
			},
			Annotations: map[string]string{},
		},
	})

	nodeops.FindPodIPFunc = func(_ context.Context, _ kubernetes.Interface, _, _, node string) (string, error) {
		if node == "node1" {
			return macIP, nil
		}
		return "", fmt.Errorf("not found")
	}

	called := false
	client.Fake.PrependReactor("patch", "nodes", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		patch := action.(k8stesting.PatchAction).GetPatch()
		if strings.Contains(string(patch), "aa:bb:cc:dd:ee:ff") {
			called = true
		}
		return false, nil, nil
	})

	nodeops.RunOnce(client, nodeops.MACUpdaterConfig{
		DryRun:        false,
		Namespace:     "ns",
		PodLabel:      "label",
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels:  map[string]string{},
		Port:          0,
	})

	if !called {
		t.Error("expected patch to be called with MAC annotation")
	}
}

func TestRunOnce_DryRunSkipsPatch(t *testing.T) {
	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "drynode",
			Labels: map[string]string{
				"cba.dev/is-managed": "true",
			},
			Annotations: map[string]string{},
		},
	})

	nodeops.FindPodIPFunc = func(_ context.Context, _ kubernetes.Interface, _, _, node string) (string, error) {
		return "dummy", nil
	}
	nodeops.FetchMACFunc = func(_ context.Context, _ string, _ int) (string, error) {
		return "11:22:33:44:55:66", nil
	}

	called := false
	client.Fake.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		called = true
		return false, nil, nil
	})

	nodeops.RunOnce(client, nodeops.MACUpdaterConfig{
		DryRun:        true,
		Namespace:     "ns",
		PodLabel:      "label",
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels:  map[string]string{},
		Port:          1234,
	})

	if called {
		t.Error("expected no patch call in dry-run mode")
	}
}
