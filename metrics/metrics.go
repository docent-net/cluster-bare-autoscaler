package metrics

import (
	"github.com/docent-net/cluster-bare-autoscaler/utils/errors"
	_ "k8s.io/component-base/metrics/prometheus/restclient" // for client-go metrics registration
	"k8s.io/klog/v2"
	"time"

	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const (
	caNamespace           = "cluster_bare_autoscaler"
	readyLabel            = "ready"
	unreadyLabel          = "unready"
	startingLabel         = "notStarted"
	unregisteredLabel     = "unregistered"
	longUnregisteredLabel = "longUnregistered"

	// LogLongDurationThreshold defines the duration after which long function
	// duration will be logged (in addition to being counted in metric).
	// This is meant to help find unexpectedly long function execution times for
	// debugging purposes.
	LogLongDurationThreshold = 5 * time.Second
)

// Names of Cluster Autoscaler operations
const (
	Main      FunctionLabel = "main"
	ScaleDown FunctionLabel = "scaleDown"
	LoopWait  FunctionLabel = "loopWait"
)

// FunctionLabel is a name of Cluster Bare Autoscaler operation for which
// we measure duration
type FunctionLabel string

var (
	nodesCount = k8smetrics.NewGaugeVec(
		&k8smetrics.GaugeOpts{
			Namespace: caNamespace,
			Name:      "nodes_count",
			Help:      "Number of nodes in cluster.",
		}, []string{"state"},
	)
	maxNodesCount = k8smetrics.NewGauge(
		&k8smetrics.GaugeOpts{
			Namespace: caNamespace,
			Name:      "max_nodes_count",
			Help:      "Maximum number of nodes in all node groups",
		},
	)

	/**** Metrics related to autoscaler execution ****/
	lastActivity = k8smetrics.NewGaugeVec(
		&k8smetrics.GaugeOpts{
			Namespace: caNamespace,
			Name:      "last_activity",
			Help:      "Last time certain part of CA logic executed.",
		}, []string{"activity"},
	)

	functionDuration = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Namespace: caNamespace,
			Name:      "function_duration_seconds",
			Help:      "Time taken by various parts of CA main loop.",
			Buckets:   k8smetrics.ExponentialBuckets(0.01, 1.5, 30), // 0.01, 0.015, 0.0225, ..., 852.2269299239293, 1278.3403948858938
		}, []string{"function"},
	)

	functionDurationSummary = k8smetrics.NewSummaryVec(
		&k8smetrics.SummaryOpts{
			Namespace: caNamespace,
			Name:      "function_duration_quantile_seconds",
			Help:      "Quantiles of time taken by various parts of CA main loop.",
			MaxAge:    time.Hour,
		}, []string{"function"},
	)

	/**** Metrics related to autoscaler operations ****/
	errorsCount = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Namespace: caNamespace,
			Name:      "errors_total",
			Help:      "The number of CA loops failed due to an error.",
		}, []string{"type"},
	)
)

// RegisterAll registers all metrics.
func RegisterAll() {
	legacyregistry.MustRegister(nodesCount)
}

// RegisterError records any errors preventing Cluster Bare Autoscaler from working.
// No more than one error should be recorded per loop.
func RegisterError(err errors.AutoscalerError) {
	errorsCount.WithLabelValues(string(err.Type())).Add(1.0)
}

// UpdateNodesCount records the number of nodes in cluster
func UpdateNodesCount(ready, unready, starting, longUnregistered, unregistered int) {
	nodesCount.WithLabelValues(readyLabel).Set(float64(ready))
	nodesCount.WithLabelValues(unreadyLabel).Set(float64(unready))
	nodesCount.WithLabelValues(startingLabel).Set(float64(starting))
	nodesCount.WithLabelValues(longUnregisteredLabel).Set(float64(longUnregistered))
	nodesCount.WithLabelValues(unregisteredLabel).Set(float64(unregistered))
}

// UpdateLastTime records the time the step identified by the label was started
func UpdateLastTime(label FunctionLabel, now time.Time) {
	lastActivity.WithLabelValues(string(label)).Set(float64(now.Unix()))
}

// UpdateMaxNodesCount records the current maximum number of nodes being set for all node groups
func UpdateMaxNodesCount(nodesCount int) {
	maxNodesCount.Set(float64(nodesCount))
}

// UpdateDurationFromStart records the duration of the step identified by the
// label using start time
func UpdateDurationFromStart(label FunctionLabel, start time.Time) {
	duration := time.Now().Sub(start)
	UpdateDuration(label, duration)
}

// UpdateDuration records the duration of the step identified by the label
func UpdateDuration(label FunctionLabel, duration time.Duration) {
	if duration > LogLongDurationThreshold && label != ScaleDown {
		klog.V(4).Infof("Function %s took %v to complete", label, duration)
	}
	functionDuration.WithLabelValues(string(label)).Observe(duration.Seconds())
	functionDurationSummary.WithLabelValues(string(label)).Observe(duration.Seconds())
}
