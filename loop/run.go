package loop

import (
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/metrics"
	"github.com/docent-net/cluster-bare-autoscaler/utils/errors"
)

type autoscaler interface {
	// RunOnce represents an iteration in the control-loop of CA.
	RunOnce(currentTime time.Time) errors.AutoscalerError
}

// RunAutoscalerOnce triggers a single autoscaling iteration.
func RunAutoscalerOnce(autoscaler autoscaler, healthCheck *metrics.HealthCheck, loopStart time.Time) {
	metrics.UpdateLastTime(metrics.Main, loopStart)
	healthCheck.UpdateLastActivity(loopStart)

	err := autoscaler.RunOnce(loopStart)
	if err != nil && err.Type() != errors.TransientError {
		metrics.RegisterError(err)
	} else {
		healthCheck.UpdateLastSuccessfulRun(time.Now())
	}

	metrics.UpdateDurationFromStart(metrics.Main, loopStart)
}
