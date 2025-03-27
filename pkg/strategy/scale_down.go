package strategy

import (
	"context"
)

// ScaleDownStrategy evaluates if a node should be scaled down.
type ScaleDownStrategy interface {
	ShouldScaleDown(ctx context.Context, nodeName string) (bool, error)
}
