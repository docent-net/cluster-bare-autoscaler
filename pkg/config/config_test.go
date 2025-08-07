package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
)

func TestLoad_ValidConfig(t *testing.T) {
	yaml := `
macDiscoveryIntervalMin: 45m
`

	tmp, err := os.CreateTemp("", "valid-config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(yaml)
	tmp.Close()

	cfg, err := config.Load(tmp.Name())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.MACDiscoveryInterval != 45*time.Minute {
		t.Errorf("expected MACDiscoveryInterval to be 45m, got %v", cfg.MACDiscoveryInterval)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got none")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	tmp, err := os.CreateTemp("", "invalid-config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString("{this: is, not: valid yaml") // missing closing }
	tmp.Close()

	_, err = config.Load(tmp.Name())
	if err == nil {
		t.Fatal("expected YAML unmarshal error, got none")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyDefaultsAndValidate_DefaultsApplied(t *testing.T) {
	cfg := &config.Config{}
	err := cfg.ApplyDefaultsAndValidate()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.MACDiscoveryInterval != 30*time.Minute {
		t.Errorf("expected default MACDiscoveryInterval to be 30m, got %v", cfg.MACDiscoveryInterval)
	}
}

func TestApplyDefaultsAndValidate_TooShort(t *testing.T) {
	cfg := &config.Config{MACDiscoveryInterval: 5 * time.Second}
	err := cfg.ApplyDefaultsAndValidate()
	if err == nil {
		t.Fatal("expected error for short MACDiscoveryInterval, got none")
	}
}
