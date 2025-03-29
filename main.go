package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/kubeclient"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/metrics"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/tracing"
)

var version = "dev"

func main() {
	slog.Info("Starting cluster-bare-autoscaler", "version", version)

	var (
		configPath string
		dryRunFlag bool
	)
	flag.StringVar(&configPath, "config", "./config.yaml", "Path to config file")
	flag.BoolVar(&dryRunFlag, "dry-run", false, "Run without making actual changes")
	flag.Parse()

	if err := tracing.Init("cluster-bare-autoscaler"); err != nil {
		slog.Error("failed to init tracing", "err", err)
		os.Exit(1)
	}
	defer tracing.Shutdown(context.Background())

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

	r := controller.NewReconciler(cfg, clientset)
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
	}
}
