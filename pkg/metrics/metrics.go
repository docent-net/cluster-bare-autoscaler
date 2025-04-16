package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

var (
	Evaluations = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_evaluation_total",
			Help: "Number of reconcile loops run",
		},
	)
	ScaleDowns = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_scale_down_total",
			Help: "Number of nodes selected for scale-down",
		},
	)
	ShutdownAttempts = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_shutdown_attempts_total",
			Help: "Number of node shutdown attempts",
		},
	)
	ShutdownSuccesses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_shutdown_success_total",
			Help: "Number of successful node shutdowns",
		},
	)
	EvictionFailures = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_eviction_failures_total",
			Help: "Number of eviction failures during drain",
		},
	)
	PoweredOffNodes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cba_powered_off_nodes",
		Help: "Number of nodes currently marked as powered off",
	}, []string{"node"})
	PowerOnAttempts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "power_on_attempts_total",
		Help: "Number of power-on attempts",
	})
	PowerOnSuccesses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "power_on_successes_total",
		Help: "Number of successful power-ons",
	})
)

type Interface interface {
	RecordEligibleNodes(int)
	RecordChosenNode(string)
}

type DefaultMetrics struct{}

func (d *DefaultMetrics) RecordEligibleNodes(_ int) {
	// testing
}

func (d *DefaultMetrics) RecordChosenNode(_ string) {
	// testing
}

func Init() {
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":9090", nil)
}
