package kubeclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeDir(t *testing.T) {
	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)

	// Test case: HOME is set
	os.Setenv("HOME", "/custom/home")
	if got := homeDir(); got != "/custom/home" {
		t.Errorf("expected '/custom/home', got '%s'", got)
	}

	// Test case: HOME is empty, fallback to USERPROFILE
	os.Setenv("HOME", "")
	os.Setenv("USERPROFILE", "/somewhere/user/example")
	if got := homeDir(); got != "/somewhere/user/example" {
		t.Errorf("expected '/somewhere/user/example', got '%s'", got)
	}
}

func TestGetRestConfig_LocalFallback(t *testing.T) {
	// Simulate environment without in-cluster config
	os.Unsetenv("KUBERNETES_SERVICE_HOST")
	os.Unsetenv("KUBERNETES_SERVICE_PORT")

	// Create temp dir to mock home dir
	tmpHome, err := os.MkdirTemp("", "fake-home")
	if err != nil {
		t.Fatalf("failed to create temp home dir: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	fakeKubeDir := filepath.Join(tmpHome, ".kube")
	if err := os.MkdirAll(fakeKubeDir, 0755); err != nil {
		t.Fatalf("failed to create fake kube dir: %v", err)
	}

	// Create dummy kubeconfig
	kubeconfig := filepath.Join(fakeKubeDir, "config")
	err = os.WriteFile(kubeconfig, []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://localhost
  name: local
contexts:
- context:
    cluster: local
    user: dev
  name: local-context
current-context: local-context
users:
- name: dev
  user:
    username: dev
    password: dev
`), 0644)
	if err != nil {
		t.Fatalf("failed to write dummy kubeconfig: %v", err)
	}

	// Override HOME
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tmpHome)

	cfg, err := GetRestConfig()
	if err != nil {
		t.Errorf("expected successful fallback config, got error: %v", err)
	}
	if cfg == nil {
		t.Errorf("expected non-nil config")
	}
}
