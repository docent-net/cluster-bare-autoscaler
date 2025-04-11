package power_test

import (
	"context"
	"testing"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/power"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewControllersFromConfig(t *testing.T) {
	client := fake.NewSimpleClientset()

	cfg := &config.Config{
		ShutdownMode: "http",
		PowerOnMode:  "wol",
		NodeLabels: config.NodeLabelConfig{
			Managed:  "cba.dev/is-managed",
			Disabled: "cba.dev/disabled",
		},
		NodeAnnotations: config.NodeAnnotationConfig{
			MAC: "cba.dev/mac-address",
		},
		WOLBootTimeoutSec: 10,
		WOLBroadcastAddr:  "255.255.255.255",
		WolAgent: config.WolAgentConfig{
			Namespace: "default",
			PodLabel:  "wol-agent",
			Port:      9100,
		},
		ShutdownManager: config.ShutdownManagerConfig{
			Namespace: "default",
			Port:      8080,
			PodLabel:  "shutdown",
		},
	}

	shutdowner, powerOner := power.NewControllersFromConfig(cfg, client)

	if shutdowner == nil {
		t.Errorf("Expected shutdown controller, got nil")
	}
	if powerOner == nil {
		t.Errorf("Expected power-on controller, got nil")
	}
}

func TestNoopControllers(t *testing.T) {
	ctx := context.Background()

	p := &power.NoopPowerOnController{}
	if err := p.PowerOn(ctx, "node1"); err != nil {
		t.Errorf("NoopPowerOnController should not fail, got: %v", err)
	}

	s := &power.NoopShutdownController{}
	if err := s.Shutdown(ctx, "node1"); err != nil {
		t.Errorf("NoopShutdownController should not fail, got: %v", err)
	}
}
