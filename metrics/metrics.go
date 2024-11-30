package metrics

import (
	_ "k8s.io/component-base/metrics/prometheus/restclient" // for client-go metrics registration

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
)

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
)

// RegisterAll registers all metrics.
func RegisterAll() {
	legacyregistry.MustRegister(nodesCount)
}

// UpdateNodesCount records the number of nodes in cluster
func UpdateNodesCount(ready, unready, starting, longUnregistered, unregistered int) {
	nodesCount.WithLabelValues(readyLabel).Set(float64(ready))
	nodesCount.WithLabelValues(unreadyLabel).Set(float64(unready))
	nodesCount.WithLabelValues(startingLabel).Set(float64(starting))
	nodesCount.WithLabelValues(longUnregisteredLabel).Set(float64(longUnregistered))
	nodesCount.WithLabelValues(unregisteredLabel).Set(float64(unregistered))
}

// UpdateMaxNodesCount records the current maximum number of nodes being set for all node groups
func UpdateMaxNodesCount(nodesCount int) {
	maxNodesCount.Set(float64(nodesCount))
}
