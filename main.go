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

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "./config.yaml", "Path to config file")
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
	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	http.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	go func() {
		slog.Info("Starting health endpoints on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			slog.Error("health endpoint server crashed", "err", err)
		}
	}()
}
