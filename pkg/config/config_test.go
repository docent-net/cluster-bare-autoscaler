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

func TestLoad_RotationBlock_Parses(t *testing.T) {
	yaml := `
rotation:
  enabled: true
  maxPoweredOffDuration: 168h
  exemptLabel: rotation-exempt
`
	tmp, err := os.CreateTemp("", "cfg-rotation-*.yaml")
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
	if !cfg.Rotation.Enabled {
		t.Fatalf("expected rotation.enabled=true")
	}
	if cfg.Rotation.MaxPoweredOffDuration != 168*time.Hour {
		t.Fatalf("expected 168h, got %v", cfg.Rotation.MaxPoweredOffDuration)
	}
	if cfg.Rotation.ExemptLabel != "rotation-exempt" {
		t.Fatalf("expected exemptLabel=rotation-exempt, got %q", cfg.Rotation.ExemptLabel)
	}
}

func TestLoad_RotationBlock_Omitted_DefaultsStayZero(t *testing.T) {
	yaml := `
# no rotation block on purpose
`
	tmp, err := os.CreateTemp("", "cfg-rotation-empty-*.yaml")
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
	if cfg.Rotation.Enabled {
		t.Fatalf("expected rotation.enabled=false by default")
	}
	if cfg.Rotation.MaxPoweredOffDuration != 0 {
		t.Fatalf("expected rotation.maxPoweredOffDuration=0 by default, got %v", cfg.Rotation.MaxPoweredOffDuration)
	}
	if cfg.Rotation.ExemptLabel != "" {
		t.Fatalf("expected rotation.exemptLabel empty by default, got %q", cfg.Rotation.ExemptLabel)
	}
}

func TestLoad_RotationBlock_InvalidDuration_Fails(t *testing.T) {
	yaml := `
rotation:
  enabled: true
  maxPoweredOffDuration: "definitely-not-a-duration"
`
	tmp, err := os.CreateTemp("", "cfg-rotation-bad-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(yaml)
	tmp.Close()

	_, err = config.Load(tmp.Name())
	if err == nil {
		t.Fatal("expected error for invalid duration, got none")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "duration") {
		t.Fatalf("expected duration-related error, got: %v", err)
	}
}
