package strategy

import "context"

type AlwaysScaleDown struct{}

func (a *AlwaysScaleDown) ShouldScaleDown(ctx context.Context, nodeName string) (bool, error) {
	return true, nil
}
