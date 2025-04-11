package power

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type ShutdownHTTPController struct {
	DryRun    bool
	Port      int
	Namespace string
	PodLabel  string
	Client    kubernetes.Interface
}

func (s *ShutdownHTTPController) Shutdown(ctx context.Context, node string) error {
	if s.DryRun {
		slog.Info("Dry-run: would shut down via HTTP", "node", node)
		return nil
	}

	podIP, err := s.FindShutdownPodIP(ctx, node)
	if err != nil {
		return err
	}

	return s.SendShutdownRequest(ctx, podIP, node)
}

func (s *ShutdownHTTPController) FindShutdownPodIP(ctx context.Context, node string) (string, error) {
	pods, err := s.Client.CoreV1().Pods(s.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set(map[string]string{
			"app": s.PodLabel,
		}).String(),
	})
	if err != nil {
		return "", fmt.Errorf("listing pods: %w", err)
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName == node {
			return pod.Status.PodIP, nil
		}
	}

	return "", fmt.Errorf("no shutdown pod found on node %s", node)
}

func (s *ShutdownHTTPController) SendShutdownRequest(ctx context.Context, podIP, node string) error {
	url := fmt.Sprintf("http://%s:%d/shutdown", podIP, s.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("creating shutdown request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling shutdown endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("shutdown request failed: %s", string(body))
	}

	slog.Info("Shutdown request sent successfully", "node", node)
	return nil
}

func (s *ShutdownHTTPController) PowerOn(ctx context.Context, node string) error {
	slog.Info("PowerOn not implemented in ShutdownHTTPController", "node", node)
	return nil
}
