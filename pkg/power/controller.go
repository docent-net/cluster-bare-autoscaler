package power

import "context"

const (
	ShutdownModeDisabled = "disabled"
	ShutdownModeHTTP     = "http"
)

type PowerController interface {
	Shutdown(ctx context.Context, nodeName string) error
	PowerOn(ctx context.Context, nodeName string) error
}
