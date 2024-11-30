package main

import (
	ctx "context"
	"flag"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/config"
	"github.com/docent-net/cluster-bare-autoscaler/core"
	"github.com/docent-net/cluster-bare-autoscaler/loop"
	"github.com/docent-net/cluster-bare-autoscaler/metrics"
	kube_util "github.com/docent-net/cluster-bare-autoscaler/utils/kubernetes"
	"github.com/docent-net/cluster-bare-autoscaler/utils/units"
	"github.com/docent-net/cluster-bare-autoscaler/version"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/component-base/logs"
	"k8s.io/component-base/metrics/legacyregistry"
	"os"
	"os/signal"
	"syscall"

	//kubelet_config "k8s.io/kubernetes/pkg/kubelet/apis/config"
	//scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"net/http"
	"strconv"
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

	coresTotal  = flag.String("cores-total", minMaxFlagString(0, config.DefaultMaxClusterCores), "Minimum and maximum number of cores in cluster, in the format <min>:<max>. Cluster autoscaler will not scale the cluster beyond these numbers.")
	memoryTotal = flag.String("memory-total", minMaxFlagString(0, config.DefaultMaxClusterMemory), "Minimum and maximum number of gigabytes of memory in cluster, in the format <min>:<max>. Cluster autoscaler will not scale the cluster beyond these numbers.")

	scanInterval     = flag.Duration("scan-interval", config.DefaultScanInterval, "How often cluster is reevaluated for scale up or down")
	scaleDownEnabled = flag.Bool("scale-down-enabled", true, "Should CBA scale down the cluster")

	maxInactivityTimeFlag = flag.Duration("max-inactivity", 10*time.Minute, "Maximum time from last recorded autoscaler activity before automatic restart")
	maxFailingTimeFlag    = flag.Duration("max-failing-time", 15*time.Minute, "Maximum time from last recorded successful autoscaler run before automatic restart")

	kubeClientBurst = flag.Int("kube-client-burst", rest.DefaultBurst, "Burst value for kubernetes client.")
	kubeClientQPS   = flag.Float64("kube-client-qps", float64(rest.DefaultQPS), "QPS value for kubernetes client.")
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

func minMaxFlagString(min, max int64) string {
	return fmt.Sprintf("%v:%v", min, max)
}

func parseMinMaxFlag(flag string) (int64, int64, error) {
	tokens := strings.SplitN(flag, ":", 2)
	if len(tokens) != 2 {
		return 0, 0, fmt.Errorf("wrong nodes configuration: %s", flag)
	}

	min, err := strconv.ParseInt(tokens[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to set min size: %s, expected integer, err: %v", tokens[0], err)
	}

	max, err := strconv.ParseInt(tokens[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to set max size: %s, expected integer, err: %v", tokens[1], err)
	}

	err = validateMinMaxFlag(min, max)
	if err != nil {
		return 0, 0, err
	}

	return min, max, nil
}

func validateMinMaxFlag(min, max int64) error {
	if min < 0 {
		return fmt.Errorf("min size must be greater or equal to  0")
	}
	if max < min {
		return fmt.Errorf("max size must be greater or equal to min size")
	}
	return nil
}

func createAutoscalingOptions() config.AutoscalingOptions {
	minCoresTotal, maxCoresTotal, err := parseMinMaxFlag(*coresTotal)
	if err != nil {
		klog.Fatalf("Failed to parse flags: %v", err)
	}
	minMemoryTotal, maxMemoryTotal, err := parseMinMaxFlag(*memoryTotal)
	if err != nil {
		klog.Fatalf("Failed to parse flags: %v", err)
	}
	// Convert memory limits to bytes.
	minMemoryTotal = minMemoryTotal * units.GiB
	maxMemoryTotal = maxMemoryTotal * units.GiB

	return config.AutoscalingOptions{
		MaxCoresTotal:    maxCoresTotal,
		MinCoresTotal:    minCoresTotal,
		MaxMemoryTotal:   maxMemoryTotal,
		MinMemoryTotal:   minMemoryTotal,
		ScaleDownEnabled: *scaleDownEnabled,
		ConfigNamespace:  *namespace,
		ClusterName:      *clusterName,
	}
}

func buildAutoscaler(context ctx.Context) (core.Autoscaler, *loop.LoopTrigger, error) {
	// Create basic config from flags.
	autoscalingOptions := createAutoscalingOptions()

	autoscalingOptions.KubeClientOpts.KubeClientBurst = int(*kubeClientBurst)
	autoscalingOptions.KubeClientOpts.KubeClientQPS = float32(*kubeClientQPS)
	kubeClient := kube_util.CreateKubeClient(autoscalingOptions.KubeClientOpts)

	// Informer transform to trim ManagedFields for memory efficiency.
	trim := func(obj interface{}) (interface{}, error) {
		if accessor, err := meta.Accessor(obj); err == nil {
			accessor.SetManagedFields(nil)
		}
		return obj, nil
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(kubeClient, 0, informers.WithTransform(trim))

	opts := core.AutoscalerOptions{
		AutoscalingOptions: autoscalingOptions,
		KubeClient:         kubeClient,
		InformerFactory:    informerFactory,
	}

	// Create autoscaler.
	autoscaler, err := core.NewAutoscaler(opts, informerFactory)
	if err != nil {
		return nil, nil, err
	}

	// Start informers. This must come after fully constructing the autoscaler because
	// additional informers might have been registered in the factory during NewAutoscaler.
	stop := make(chan struct{})
	informerFactory.Start(stop)

	podObserver := loop.StartPodObserver(context, kube_util.CreateKubeClient(autoscalingOptions.KubeClientOpts))

	// A ProvisioningRequestPodsInjector is used as provisioningRequestProcessingTimesGetter here to obtain the last time a
	// ProvisioningRequest was processed. This is because the ProvisioningRequestPodsInjector in addition to injecting pods
	// also marks the ProvisioningRequest as accepted or failed.
	trigger := loop.NewLoopTrigger(autoscaler, ProvisioningRequestInjector, podObserver, *scanInterval)

	return autoscaler, trigger, nil
}

func run(healthCheck *metrics.HealthCheck) {
	schedulermetrics.Register()
	metrics.RegisterAll()
	context, cancel := ctx.WithCancel(ctx.Background())
	defer cancel()

	autoscaler, trigger, err := buildAutoscaler(context)
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
	for {
		time.Sleep(*scanInterval)
		loop.RunAutoscalerOnce(autoscaler, healthCheck, time.Now())
	}
}

func registerSignalHandlers(autoscaler core.Autoscaler) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGQUIT)
	klog.V(1).Info("Registered cleanup signal handler")

	go func() {
		<-sigs
		klog.V(1).Info("Received signal, attempting cleanup")
		autoscaler.ExitCleanUp()
		klog.V(1).Info("Cleaned up, exiting...")
		klog.Flush()
		os.Exit(0)
	}()
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
