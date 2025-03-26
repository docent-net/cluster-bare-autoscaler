package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

var (
	Evaluations = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_evaluation_total",
			Help: "Number of reconcile loops run",
		},
	)
	ScaleDowns = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_scale_down_total",
			Help: "Number of nodes selected for scale-down",
		},
	)
	ShutdownAttempts = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_shutdown_attempts_total",
			Help: "Number of node shutdown attempts",
		},
	)
	ShutdownSuccesses = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_shutdown_success_total",
			Help: "Number of successful node shutdowns",
		},
	)
	EvictionFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "autoscaler_eviction_failures_total",
			Help: "Number of eviction failures during drain",
		},
	)
)

func Init() {
	prometheus.MustRegister(Evaluations, ScaleDowns, ShutdownAttempts, ShutdownSuccesses, EvictionFailures)
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":9090", nil)
}
