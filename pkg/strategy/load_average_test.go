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

// mockFetcher implements ClusterLoadFetcher
type mockFetcher struct {
	mockData []float64
}

func (m *mockFetcher) FetchClusterLoads(_ context.Context, _ []string) ([]float64, error) {
	return m.mockData, nil
}

func newTestStrategy(t *testing.T, server *httptest.Server, pods []v1.Pod) *LoadAverageScaleDown {
	clientset := corefake.NewSimpleClientset(toRuntimeObjects(pods)...)

	return &LoadAverageScaleDown{
		Client:                 clientset,
		Namespace:              "default",
		PodLabel:               "app=test-metrics",
		HTTPPort:               serverPortFromURL(server.URL),
		HTTPTimeout:            time.Second,
		NodeThreshold:          0.5,
		DryRunNodeLoadOverride: nil,
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

	strategy := newTestStrategyWithDefaults(t, "dummy-node", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.ClusterEvalMode = ClusterEvalAverage
		s.ClusterWideThreshold = 0.5
		s.DryRunClusterLoadOverride = ptr(0.3)
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "dummy-node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected scale down to be allowed with override=0.3 < 0.5")
	}
}

func TestFetchNormalizedLoad_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"load15":   1.2,
			"cpuCount": 4,
		})
	}
	strategy := newServerBackedStrategy(t, "node1", handler)

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
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json"))
	}
	strategy := newServerBackedStrategy(t, "node1", handler)

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

func TestShouldScaleDown_ClusterEvalAverage(t *testing.T) {
	override := 0.4

	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.ClusterEvalMode = ClusterEvalAverage
		s.LoadFetcher = &mockFetcher{mockData: []float64{0.6, 0.5}} // avg = 0.55
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected node1 to NOT be below cluster average (0.4 >= avg(0.6,0.5))")
	}
}

func TestShouldScaleDown_DryRunOverrideWins(t *testing.T) {
	override := 0.3

	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.ClusterEvalMode = ClusterEvalAverage
		s.ClusterWideThreshold = 0.5
		s.DryRunClusterLoadOverride = ptr(0.3)
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected node1 to be eligible for scale-down due to override")
	}
}

func TestShouldScaleDown_NoClusterData(t *testing.T) {
	override := 0.3

	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.ClusterEvalMode = ClusterEvalMedian
		s.DryRunClusterLoadOverride = ptr(0.0) // Simulate zero aggregate load
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be denied due to lack of cluster data")
	}
}

func TestShouldScaleDown_ThresholdBlocks(t *testing.T) {
	override := 1.0
	strategy := &LoadAverageScaleDown{
		DryRunNodeLoadOverride: &override,
		NodeThreshold:          0.5,
	}

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be denied due to high override load")
	}
}

func newTestStrategyWithDefaults(t *testing.T, name string, opts ...func(*LoadAverageScaleDown)) *LoadAverageScaleDown {
	t.Helper()

	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	peerNode := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: name + "-peer"}}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-pod-" + name,
			Namespace: "default",
			Labels:    map[string]string{"app": "test-metrics"},
		},
		Spec:   v1.PodSpec{NodeName: name},
		Status: v1.PodStatus{PodIP: "127.0.0.1"},
	}

	peerPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-pod-" + name + "-peer",
			Namespace: "default",
			Labels:    map[string]string{"app": "test-metrics"},
		},
		Spec:   v1.PodSpec{NodeName: name + "-peer"},
		Status: v1.PodStatus{PodIP: "127.0.0.1"},
	}

	strategy := &LoadAverageScaleDown{
		Client:          corefake.NewSimpleClientset(node, peerNode, pod, peerPod),
		Namespace:       "default",
		PodLabel:        "app=test-metrics",
		HTTPPort:        9100,
		HTTPTimeout:     1 * time.Second,
		NodeThreshold:   0.5,
		ClusterEvalMode: ClusterEvalNone,
		IgnoreLabels:    map[string]string{},
	}

	for _, opt := range opts {
		opt(strategy)
	}

	return strategy
}

func newServerBackedStrategy(t *testing.T, nodeName string, handler http.HandlerFunc) *LoadAverageScaleDown {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	ip := server.Listener.Addr().(*net.TCPAddr).IP.String()

	clientset := corefake.NewSimpleClientset(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-pod",
			Namespace: "default",
			Labels:    map[string]string{"app": "test-metrics"},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
		Status: v1.PodStatus{
			PodIP: ip,
		},
	})

	return &LoadAverageScaleDown{
		Client:                 clientset,
		Namespace:              "default",
		PodLabel:               "app=test-metrics",
		HTTPPort:               serverPortFromURL(server.URL),
		HTTPTimeout:            1 * time.Second,
		NodeThreshold:          0.5,
		DryRunNodeLoadOverride: nil,
	}
}

func TestAggregationFunctions(t *testing.T) {
	cases := []struct {
		name     string
		fn       func([]float64) float64
		input    []float64
		expected float64
	}{
		{
			name:     "Average of 1,2,3",
			fn:       average,
			input:    []float64{1, 2, 3},
			expected: 2.0,
		},
		{
			name:     "Median odd",
			fn:       median,
			input:    []float64{5, 1, 3},
			expected: 3.0,
		},
		{
			name:     "Median even",
			fn:       median,
			input:    []float64{1, 2, 3, 4},
			expected: 2.5,
		},
		{
			name:     "P90 rounded down",
			fn:       p90,
			input:    []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			expected: 10.0,
		},
		{
			name:     "P90 short list",
			fn:       p90,
			input:    []float64{10, 20, 30},
			expected: 30.0, // 90th percentile of 3 values is the last one
		},
		{
			name:     "Empty input returns 0",
			fn:       average,
			input:    []float64{},
			expected: 0,
		},
		{
			name:     "Empty input returns 0 (median)",
			fn:       median,
			input:    []float64{},
			expected: 0,
		},
		{
			name:     "Empty input returns 0 (p90)",
			fn:       p90,
			input:    []float64{},
			expected: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn(tc.input)
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestShouldScaleDown_ClusterWideThresholdBlocks(t *testing.T) {
	override := 0.3

	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.NodeThreshold = 0.5
		s.ClusterWideThreshold = 0.4
		s.ClusterEvalMode = ClusterEvalAverage
		s.DryRunClusterLoadOverride = ptr(0.55) // aggregate = 0.55 (too high)
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be denied due to high cluster-wide load (0.55 >= 0.4)")
	}

	// Now test passing cluster-wide threshold
	strategy.DryRunClusterLoadOverride = ptr(0.25) // aggregate = 0.25 (ok)

	ok, err = strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected scale-down to be allowed (0.25 < 0.4)")
	}
}

func ptr[T any](v T) *T {
	return &v
}
