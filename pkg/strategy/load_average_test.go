package strategy

import (
	"context"
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	corefake "k8s.io/client-go/kubernetes/fake"
)

type testStrategy struct {
	LoadAverageScaleDown
}

func newTestStrategy(t *testing.T, server *httptest.Server, pods []v1.Pod) *LoadAverageScaleDown {
	clientset := corefake.NewSimpleClientset(toRuntimeObjects(pods)...)

	return &LoadAverageScaleDown{
		Client:         clientset,
		Namespace:      "default",
		PodLabel:       "app=test-metrics",
		HTTPPort:       serverPortFromURL(server.URL),
		HTTPTimeout:    time.Second,
		Threshold:      0.5,
		DryRunOverride: nil,
	}
}

func toRuntimeObjects(pods []v1.Pod) []runtime.Object {
	var objs []runtime.Object
	for _, pod := range pods {
		obj := pod.DeepCopy()
		objs = append(objs, obj)
	}
	return objs
}

func serverPortFromURL(url string) int {
	_, port, _ := net.SplitHostPort(url[len("http://"):])
	p, _ := strconv.Atoi(port)
	return p
}

func TestDryRunOverride(t *testing.T) {
	override := 0.3
	strategy := &LoadAverageScaleDown{
		DryRunOverride: &override,
		Threshold:      0.5,
	}

	ok, err := strategy.ShouldScaleDown(context.Background(), "dummy-node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected scale down to be allowed with override=0.3 < 0.5")
	}
}

func TestFetchNormalizedLoad_Success(t *testing.T) {
	mockData := map[string]interface{}{
		"load15":   1.2,
		"cpuCount": 4,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mockData)
	}))
	defer server.Close()

	ip := server.Listener.Addr().(*net.TCPAddr).IP.String()

	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-pod",
			Labels:    map[string]string{"app": "test-metrics"},
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			NodeName: "node1",
		},
		Status: v1.PodStatus{
			PodIP: ip,
		},
	}

	strategy := &LoadAverageScaleDown{
		Client:         corefake.NewSimpleClientset(&pod),
		Namespace:      "default",
		PodLabel:       "app=test-metrics",
		HTTPPort:       serverPortFromURL(server.URL),
		HTTPTimeout:    1 * time.Second,
		Threshold:      0.5,
		DryRunOverride: nil,
	}

	load, err := strategy.fetchNormalizedLoad(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := 1.2 / 4.0
	if load != expected {
		t.Errorf("expected normalized load %v, got %v", expected, load)
	}
}

func TestFetchNormalizedLoad_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	strategy := newTestStrategy(t, server, []v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "metrics-pod",
				Labels: map[string]string{"app": "test-metrics"},
			},
			Spec: v1.PodSpec{NodeName: "node1"},
			Status: v1.PodStatus{
				PodIP: server.Listener.Addr().(*net.TCPAddr).IP.String(),
			},
		},
	})

	_, err := strategy.fetchNormalizedLoad(context.Background(), "node1")
	if err == nil {
		t.Fatal("expected error due to bad JSON")
	}
}

func TestFetchNormalizedLoad_MissingPod(t *testing.T) {
	strategy := &LoadAverageScaleDown{
		Client:      corefake.NewSimpleClientset(), // no pods
		Namespace:   "default",
		PodLabel:    "app=test-metrics",
		HTTPPort:    9100,
		HTTPTimeout: time.Second,
	}

	_, err := strategy.fetchNormalizedLoad(context.Background(), "node1")
	if err == nil {
		t.Fatal("expected error due to missing metrics pod")
	}
}
