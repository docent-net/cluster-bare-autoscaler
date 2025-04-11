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

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/power"
)

func TestFindShutdownPodIP_Success(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shutdown-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app": "shutdown",
			},
		},
		Spec: v1.PodSpec{
			NodeName: "node1",
		},
		Status: v1.PodStatus{
			PodIP: "10.0.0.42",
		},
	})

	ctrl := &power.ShutdownHTTPController{
		Client:    client, // ✅ use the variable, not the type name
		Namespace: "default",
		PodLabel:  "shutdown",
		Port:      8080,
	}

	ip, err := ctrl.FindShutdownPodIP(context.Background(), "node1")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if ip != "10.0.0.42" {
		t.Errorf("expected IP 10.0.0.42, got: %s", ip)
	}
}

func TestFindShutdownPodIP_NotFound(t *testing.T) {
	client := corefake.NewSimpleClientset()

	ctrl := &power.ShutdownHTTPController{
		Client:    client,
		Namespace: "default",
		PodLabel:  "shutdown",
		Port:      8080,
	}

	_, err := ctrl.FindShutdownPodIP(context.Background(), "node1")
	if err == nil || !strings.Contains(err.Error(), "no shutdown pod found") {
		t.Errorf("expected shutdown pod not found error, got: %v", err)
	}
}

func TestSendShutdownRequest_Success(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shutdown" {
			called = true
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// Parse host and port from server.URL
	u, _ := url.Parse(server.URL)
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)

	ctrl := &power.ShutdownHTTPController{
		Port: port, // use the correct dynamic port
	}

	err := ctrl.SendShutdownRequest(context.Background(), host, "node2")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !called {
		t.Errorf("expected shutdown handler to be called")
	}
}

func TestSendShutdownRequest_Failure(t *testing.T) {
	ctrl := &power.ShutdownHTTPController{
		Port: 65534, // very unlikely to be open
	}
	err := ctrl.SendShutdownRequest(context.Background(), "localhost", "node1")
	if err == nil {
		t.Errorf("expected error when sending shutdown request to unreachable port")
	}
}
