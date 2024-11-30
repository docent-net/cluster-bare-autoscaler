package main

import (
	ctx "context"
	"flag"
	"github.com/docent-net/cluster-bare-autoscaler/metrics"
	"github.com/docent-net/cluster-bare-autoscaler/version"
	"github.com/spf13/pflag"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/component-base/logs"
	"k8s.io/component-base/metrics/legacyregistry"
	"net/http"
	"strings"
	"time"

	utilfeature "k8s.io/apiserver/pkg/util/feature"

	kube_flag "k8s.io/component-base/cli/flag"
	logsapi "k8s.io/component-base/logs/api/v1"
	_ "k8s.io/component-base/logs/json/register"
	"k8s.io/klog/v2"

	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
)

// MultiStringFlag is a flag for passing multiple parameters using same flag
type MultiStringFlag []string

// String returns string representation of the node groups.
func (flag *MultiStringFlag) String() string {
	return "[" + strings.Join(*flag, " ") + "]"
}

// Set adds a new configuration.
func (flag *MultiStringFlag) Set(value string) error {
	*flag = append(*flag, value)
	return nil
}

func multiStringFlag(name string, usage string) *MultiStringFlag {
	value := new(MultiStringFlag)
	flag.Var(value, name, usage)
	return value
}

var (
	clusterName    = flag.String("cluster-name", "", "Autoscaled cluster name, if available")
	address        = flag.String("address", ":8085", "The address to expose prometheus metrics.")
	kubeConfigFile = flag.String("kubeconfig", "", "Path to kubeconfig file with authorization and master location information.")
	namespace      = flag.String("namespace", "kube-system", "Namespace in which cluster-autoscaler run.")

	scaleDownEnabled = flag.Bool("scale-down-enabled", true, "Should CA scale down the cluster")

	maxInactivityTimeFlag = flag.Duration("max-inactivity", 10*time.Minute, "Maximum time from last recorded autoscaler activity before automatic restart")
	maxFailingTimeFlag    = flag.Duration("max-failing-time", 15*time.Minute, "Maximum time from last recorded successful autoscaler run before automatic restart")
)

func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func run(healthCheck *metrics.HealthCheck) {
	schedulermetrics.Register()
	metrics.RegisterAll()
	context, cancel := ctx.WithCancel(ctx.Background())
	defer cancel()

	autoscaler, trigger, err := buildAutoscaler(context, debuggingSnapshotter)
	if err != nil {
		klog.Fatalf("Failed to create autoscaler: %v", err)
	}

	// Register signal handlers for graceful shutdown.
	registerSignalHandlers(autoscaler)

	// Start updating health check endpoint.
	healthCheck.StartMonitoring()

	// Start components running in background.
	if err := autoscaler.Start(); err != nil {
		klog.Fatalf("Failed to autoscaler background components: %v", err)
	}

	// Autoscale ad infinitum.
	if *frequentLoopsEnabled {
		lastRun := time.Now()
		for {
			trigger.Wait(lastRun)
			lastRun = time.Now()
			loop.RunAutoscalerOnce(autoscaler, healthCheck, lastRun)
		}
	} else {
		for {
			time.Sleep(*scanInterval)
			loop.RunAutoscalerOnce(autoscaler, healthCheck, time.Now())
		}
	}
}

func main() {
	klog.InitFlags(nil)

	featureGate := utilfeature.DefaultMutableFeatureGate
	loggingConfig := logsapi.NewLoggingConfiguration()

	if err := logsapi.AddFeatureGates(featureGate); err != nil {
		klog.Fatalf("Failed to add logging feature flags: %v", err)
	}

	logsapi.AddFlags(loggingConfig, pflag.CommandLine)
	featureGate.AddFlag(pflag.CommandLine)
	kube_flag.InitFlags()

	logs.InitLogs()
	if err := logsapi.ValidateAndApply(loggingConfig, featureGate); err != nil {
		klog.Fatalf("Failed to validate and apply logging configuration: %v", err)
	}

	healthCheck := metrics.NewHealthCheck(*maxInactivityTimeFlag, *maxFailingTimeFlag)

	klog.V(1).Infof("Cluster Autoscaler %s", version.ClusterAutoscalerVersion)

	go func() {
		pathRecorderMux := mux.NewPathRecorderMux("cluster-bare-autoscaler")
		defaultMetricsHandler := legacyregistry.Handler().ServeHTTP
		pathRecorderMux.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
			defaultMetricsHandler(w, req)
		})

		pathRecorderMux.HandleFunc("/health-check", healthCheck.ServeHTTP)
		err := http.ListenAndServe(*address, pathRecorderMux)
		klog.Fatalf("Failed to start metrics: %v", err)
	}()

	run(healthCheck)
}
