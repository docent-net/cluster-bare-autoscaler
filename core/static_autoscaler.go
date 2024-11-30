package core

import (
	"github.com/docent-net/cluster-bare-autoscaler/config"
	"time"
)

const (
	// How old the oldest unschedulable pod should be before starting scale up.
	unschedulablePodTimeBuffer = 2 * time.Second
	// How old the oldest unschedulable pod with GPU should be before starting scale up.
	// The idea is that nodes with GPU are very expensive and we're ready to sacrifice
	// a bit more latency to wait for more pods and make a more informed scale-up decision.
	unschedulablePodWithGpuTimeBuffer = 30 * time.Second

	// NodeUpcomingAnnotation is an annotation CA adds to nodes which are upcoming.
	NodeUpcomingAnnotation = "cluster-autoscaler.k8s.io/upcoming-node"

	// podScaleUpDelayAnnotationKey is an annotation how long pod can wait to be scaled up.
	podScaleUpDelayAnnotationKey = "cluster-autoscaler.kubernetes.io/pod-scale-up-delay"
)

// NewStaticAutoscaler creates an instance of Autoscaler filled with provided parameters
func NewStaticAutoscaler() *StaticAutoscaler {
	return &StaticAutoscaler{
		AutoscalingContext:      autoscalingContext,
		lastScaleUpTime:         initialScaleTime,
		lastScaleDownDeleteTime: initialScaleTime,
		lastScaleDownFailTime:   initialScaleTime,
		scaleDownPlanner:        scaleDownPlanner,
		scaleDownActuator:       scaleDownActuator,
		scaleUpOrchestrator:     scaleUpOrchestrator,
		processors:              processors,
		loopStartNotifier:       loopStartNotifier,
		processorCallbacks:      processorCallbacks,
		clusterStateRegistry:    clusterStateRegistry,
		taintConfig:             taintConfig,
	}
}
