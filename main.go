package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/kubeclient"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/metrics"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/tracing"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

var version = "dev"

func main() {
	slog.Info("Starting cluster-bare-autoscaler", "version", version)

	var (
		configPath            string
		dryRunFlag            bool
		dryRunNodeLoad        float64
		dryRunClusterLoadDown float64
		dryRunClusterLoadUp   float64
	)

	flag.StringVar(&configPath, "config", "./config.yaml", "Path to config file")
	flag.BoolVar(&dryRunFlag, "dry-run", false, "Run without making actual changes")
	flag.Float64Var(&dryRunNodeLoad, "dry-run-node-load", -1, "Override normalized load for testing (0.0–1.0)")
	flag.Float64Var(&dryRunClusterLoadDown, "dry-run-cluster-load-down", -1, "Override scale-down cluster-wide load")
	flag.Float64Var(&dryRunClusterLoadUp, "dry-run-cluster-load-up", -1, "Override scale-up cluster-wide load")
	flag.Parse()

	if err := tracing.Init("cluster-bare-autoscaler"); err != nil {
		slog.Error("failed to init tracing", "err", err)
		os.Exit(1)
	}
	defer tracing.Shutdown(context.Background())

	restConfig, err := kubeclient.GetRestConfig()
	if err != nil {
		slog.Error("failed to load Kubernetes rest config", "err", err)
		os.Exit(1)
	}

	metricsClient, err := metricsclient.NewForConfig(restConfig)
	if err != nil {
		slog.Error("failed to init metrics client", "err", err)
		os.Exit(1)
	}

	metrics.Init()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// Override with CLI flag if set
	if dryRunFlag {
		cfg.DryRun = true
	}

	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	clientset, err := kubeclient.Get()
	if err != nil {
		slog.Error("failed to init k8s client", "err", err)
		os.Exit(1)
	}

	startHealthEndpoints()

	if cfg.BootstrapCooldownSeconds > 0 {
		slog.Info("Waiting for bootstrap cooldown", "seconds", cfg.BootstrapCooldownSeconds)
		time.Sleep(time.Duration(cfg.BootstrapCooldownSeconds) * time.Second)
	}

	var opts []controller.ReconcilerOption
	if dryRunNodeLoad >= 0 {
		opts = append(opts, controller.WithDryRunNodeLoad(dryRunNodeLoad))
	}
	if dryRunClusterLoadDown >= 0 {
		opts = append(opts, controller.WithDryRunClusterLoadDown(dryRunClusterLoadDown))
	}
	if dryRunClusterLoadUp >= 0 {
		opts = append(opts, controller.WithDryRunClusterLoadUp(dryRunClusterLoadUp))
	}

	go nodeops.StartMACAnnotationUpdater(clientset, nodeops.MACUpdaterConfig{
		DryRun:        cfg.DryRun,
		ManagedLabel:  cfg.NodeLabels.Managed,
		DisabledLabel: cfg.NodeLabels.Disabled,
		IgnoreLabels:  cfg.IgnoreLabels,
		Interval:      cfg.MACDiscoveryInterval,
		Namespace:     cfg.ShutdownManager.Namespace,
		PodLabel:      cfg.ShutdownManager.PodLabel,
		Port:          cfg.ShutdownManager.Port,
	})

	r := controller.NewReconciler(cfg, clientset, metricsClient, opts...)
	ctx := context.Background()
	for {
		if err := r.Reconcile(ctx); err != nil {
			slog.Error("reconcile error", "err", err)
		}
		time.Sleep(cfg.PollInterval)
	}
}

func startHealthEndpoints() {
	slog.Info("Starting health endpoints on :8080")

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	http.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			slog.Error("health endpoint server crashed", "err", err)
		}
	}()
}

func init() {
	flag.Usage = func() {
		println("Usage: cluster-bare-autoscaler [options]")
		println()
		println("Options:")
		println("  -config string")
		println("        Path to config file (default \"./config.yaml\")")
		println("  -dry-run")
		println("        Run in dry-run mode (no real actions)")
		println("  -dry-run-node-load float")
		println("        Override normalized load for testing (0.0–1.0). Skips /load lookup")
		println("  -dry-run-cluster-load-down float")
		println("        Override cluster-wide aggregate load for scale-down")
		println("  -dry-run-cluster-load-up float")
		println("        Override cluster-wide aggregate load for scale-up")
	}
}
