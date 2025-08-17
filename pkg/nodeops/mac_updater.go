package nodeops

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/client-go/kubernetes"
	"log/slog"
	"net/http"
	"time"
)

var (
	FindPodIPFunc = FindPodIPOnNode
	FetchMACFunc  = FetchMACFromDaemon
)

type MACUpdaterConfig struct {
	DryRun        bool
	Interval      time.Duration
	Port          int
	Namespace     string
	PodLabel      string
	ManagedLabel  string
	DisabledLabel string
	IgnoreLabels  map[string]string
}

func StartMACAnnotationUpdater(client kubernetes.Interface, cfg MACUpdaterConfig) {
	go func() {
		slog.Info("MAC updater started", "interval", cfg.Interval.String())
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			RunOnce(client, cfg)
			time.Sleep(10 * time.Second)
		}
	}()
}

func RunOnce(client kubernetes.Interface, cfg MACUpdaterConfig) {
	ctx := context.Background()

	nodes, err := ListManagedNodes(ctx, client, ManagedNodeFilter{
		ManagedLabel:  cfg.ManagedLabel,
		DisabledLabel: cfg.DisabledLabel,
		IgnoreLabels:  cfg.IgnoreLabels,
	})
	if err != nil {
		slog.Warn("MAC updater: failed to list managed nodes", "err", err)
		return
	}

	now := time.Now()

	for _, rawNode := range nodes {
		node := NewNodeWrapper(&rawNode, nil, now, NodeAnnotationConfig{}, nil)

		// Skip if manual override is set
		if node.HasManualMACOverride() {
			slog.Debug("Skipping MAC update for node with manual override", "node", node.Name)
			continue
		}

		// Skip if already has MAC discovered
		if node.HasDiscoveredMACAddr() {
			slog.Debug("Skipping MAC update for node with existing auto annotation", "node", node.Name)
			continue
		}

		ip, err := FindPodIPFunc(ctx, client, cfg.Namespace, cfg.PodLabel, node.Name)
		if err != nil {
			slog.Warn("MAC updater: failed to find Pod IP", "node", node.Name, "err", err)
			continue
		}

		mac, err := FetchMACFunc(ctx, ip, cfg.Port)
		if err != nil {
			slog.Warn("MAC updater: failed to fetch MAC from daemon", "node", node.Name, "err", err)
			continue
		}

		slog.Debug("Discovered MAC address", "node", node.Name, "mac", mac)

		if err := node.SetDiscoveredMAC(ctx, client, mac, cfg.DryRun); err != nil {
			continue
		}

		slog.Info("MAC annotation applied", "node", node.Name, "mac", mac)
	}
}

func FetchMACFromDaemon(ctx context.Context, ip string, port int) (string, error) {
	var url string
	if port == 0 {
		url = fmt.Sprintf("http://%s/mac", ip)
	} else {
		url = fmt.Sprintf("http://%s:%d/mac", ip, port)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating MAC request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending MAC request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding MAC response: %w", err)
	}

	return result.MAC, nil
}
