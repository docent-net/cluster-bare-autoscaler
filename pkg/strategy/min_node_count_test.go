package strategy

import (
	"context"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestMinNodeCountScaleUp(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		minNodes     int
		active       []v1.Node
		shutdown     []string
		wantNode     string
		wantDecision bool
	}{
		{
			name:         "enough nodes, do nothing",
			minNodes:     3,
			active:       []v1.Node{{}, {}, {}},
			shutdown:     []string{"node4"},
			wantNode:     "",
			wantDecision: false,
		},
		{
			name:         "below minNodes, shutdown available",
			minNodes:     3,
			active:       []v1.Node{{}, {}},
			shutdown:     []string{"node4"},
			wantNode:     "node4",
			wantDecision: true,
		},
		{
			name:         "below minNodes, no shutdown available",
			minNodes:     3,
			active:       []v1.Node{{}, {}},
			shutdown:     []string{},
			wantNode:     "",
			wantDecision: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := &MinNodeCountScaleUp{
				Cfg: &config.Config{
					MinNodes: tt.minNodes,
				},
				ActiveNodes: func(_ context.Context) ([]v1.Node, error) {
					return tt.active, nil
				},
				ShutdownList: func(_ context.Context) []string {
					return tt.shutdown
				},
			}

			gotNode, gotDecision, err := strategy.ShouldScaleUp(ctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotDecision != tt.wantDecision || gotNode != tt.wantNode {
				t.Errorf("got (%v, %q), want (%v, %q)", gotDecision, gotNode, tt.wantDecision, tt.wantNode)
			}
		})
	}
}
