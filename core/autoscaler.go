package core

import (
	"strings"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/config"

	"k8s.io/client-go/informers"
	kube_client "k8s.io/client-go/kubernetes"
)

// AutoscalerOptions is the whole set of options for configuring an autoscaler
type AutoscalerOptions struct {
	config.AutoscalingOptions
	KubeClient      kube_client.Interface
	InformerFactory informers.SharedInformerFactory
}

// Autoscaler is the main component of CA which scales up/down node groups according to its configuration
// The configuration can be injected at the creation of an autoscaler
type Autoscaler interface {
	// Start starts components running in background.
	Start() error
	// RunOnce represents an iteration in the control-loop of CA
	RunOnce(currentTime time.Time) errors.AutoscalerError
	// ExitCleanUp is a clean-up performed just before process termination.
	ExitCleanUp()
	// LastScaleUpTime is a time of the last scale up
	LastScaleUpTime() time.Time
	// LastScaleUpTime is a time of the last scale down
	LastScaleDownDeleteTime() time.Time
}

// NewAutoscaler creates an autoscaler of an appropriate type according to the parameters
func NewAutoscaler(opts AutoscalerOptions, informerFactory informers.SharedInformerFactory) (Autoscaler, errors.AutoscalerError) {
	err := initializeDefaultOptions(&opts, informerFactory)
	if err != nil {
		return nil, errors.ToAutoscalerError(errors.InternalError, err)
	}
	return NewStaticAutoscaler(
		opts.AutoscalingOptions,
		opts.PredicateChecker,
		opts.ClusterSnapshot,
		opts.AutoscalingKubeClients,
		opts.Processors,
		opts.LoopStartNotifier,
		opts.CloudProvider,
		opts.ExpanderStrategy,
		opts.EstimatorBuilder,
		opts.Backoff,
		opts.DebuggingSnapshotter,
		opts.RemainingPdbTracker,
		opts.ScaleUpOrchestrator,
		opts.DeleteOptions,
		opts.DrainabilityRules,
	), nil
}

// Initialize default options if not provided.
func initializeDefaultOptions(opts *AutoscalerOptions, informerFactory informers.SharedInformerFactory) error {

	return nil
}
