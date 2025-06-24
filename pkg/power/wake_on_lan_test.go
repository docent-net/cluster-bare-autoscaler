package power_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/power"
)

func TestWakeOnLanController_PowerOn_DryRun(t *testing.T) {
	ctrl := &power.WakeOnLanController{
		DryRun: true,
	}

	err := ctrl.PowerOn(context.Background(), "node1", "00:11:22:33:44:55")
	if err != nil {
		t.Errorf("dry-run PowerOn should not return error, got: %v", err)
	}
}

func TestWakeOnLanController_PowerOn_NoAgentPod(t *testing.T) {
	ctrl := &power.WakeOnLanController{
		Client:     corefake.NewSimpleClientset(),
		Namespace:  "default",
		PodLabel:   "wol-agent",
		MaxRetries: 1,
	}

	err := ctrl.PowerOn(context.Background(), "node1", "00:11:22:33:44:55")
	if err == nil || !strings.Contains(err.Error(), "no WOL agent pod found") {
		t.Errorf("expected missing agent pod error, got: %v", err)
	}
}

func TestWakeOnLanController_PowerOn_RequestFailure(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wol-agent",
			Namespace: "default",
			Labels:    map[string]string{"app": "wol-agent"},
		},
		Status: v1.PodStatus{
			PodIP: "localhost", // won't reach
		},
	})

	ctrl := &power.WakeOnLanController{
		Client:     client,
		Namespace:  "default",
		PodLabel:   "wol-agent",
		Port:       65534,
		MaxRetries: 1,
	}

	err := ctrl.PowerOn(context.Background(), "node1", "00:11:22:33:44:55")
	if err == nil {
		t.Errorf("expected error due to WOL request failure")
	}
}

func TestWakeOnLanController_PowerOn_WOLSuccess(t *testing.T) {
	// Fake WOL agent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ip, port := parseHostPort(t, server.URL)

	client := corefake.NewSimpleClientset(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wol-agent",
			Namespace: "default",
			Labels:    map[string]string{"app": "wol-agent"},
		},
		Status: v1.PodStatus{PodIP: ip},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{Type: v1.NodeReady, Status: v1.ConditionTrue},
			},
		},
	})

	ctrl := &power.WakeOnLanController{
		Client:         client,
		Namespace:      "default",
		PodLabel:       "wol-agent",
		Port:           port,
		BootTimeoutSec: 3 * time.Second,
		MaxRetries:     1,
	}

	err := ctrl.PowerOn(context.Background(), "node1", "00:11:22:33:44:55")
	if err != nil {
		t.Errorf("expected WOL PowerOn success, got: %v", err)
	}
}

func TestWakeOnLanController_PowerOn_ExceedsMaxRetries(t *testing.T) {
	// Agent responds, but node never ready
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ip, port := parseHostPort(t, server.URL)

	client := corefake.NewSimpleClientset(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wol-agent",
			Namespace: "default",
			Labels:    map[string]string{"app": "wol-agent"},
		},
		Status: v1.PodStatus{PodIP: ip},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Status:     v1.NodeStatus{}, // no Ready condition
	})

	ctrl := &power.WakeOnLanController{
		Client:         client,
		Namespace:      "default",
		PodLabel:       "wol-agent",
		Port:           port,
		BootTimeoutSec: 1 * time.Second,
		MaxRetries:     2,
	}

	err := ctrl.PowerOn(context.Background(), "node1", "00:11:22:33:44:55")
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("expected failure due to node never becoming ready, got: %v", err)
	}
}

// Helper: parse IP and port from httptest.Server URL
func parseHostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	urlObj, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse failed: %v", err)
	}
	host, portStr, err := net.SplitHostPort(urlObj.Host)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("invalid port: %v", err)
	}
	return host, port
}
