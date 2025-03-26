// main.go
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/kubeclient"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/metrics"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "./config.yaml", "Path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	metrics.Init()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	clientset, err := kubeclient.Get()
	if err != nil {
		slog.Error("failed to init k8s client", "err", err)
		os.Exit(1)
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
