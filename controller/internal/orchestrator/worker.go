package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
)

// Worker represents a single k6 worker instance reachable via its REST API.
type Worker struct {
	Address          string
	DashboardEnabled bool
	DashboardHost    string
	DashboardPort    int
	client           *http.Client
}

func NewWorker(address string, dashboardEnabled bool, dashboardHost string, dashboardPort int) *Worker {
	return &Worker{
		Address:          address,
		DashboardEnabled: dashboardEnabled,
		DashboardHost:    dashboardHost,
		DashboardPort:    dashboardPort,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (w *Worker) baseURL() string {
	return fmt.Sprintf("http://%s", w.Address)
}

func (w *Worker) Name() string {
	host, _, err := net.SplitHostPort(w.Address)
	if err == nil && host != "" {
		return host
	}
	return w.Address
}

func (w *Worker) DashboardURL() string {
	if !w.DashboardEnabled || w.DashboardPort <= 0 {
		return ""
	}

	host := w.Name()
	dashboardHost := w.DashboardHost
	if dashboardHost == "" || dashboardHost == "0.0.0.0" || dashboardHost == "::" {
		dashboardHost = host
	}

	return fmt.Sprintf("http://%s:%d", dashboardHost, w.DashboardPort)
}

// k6 JSON:API envelope types

type k6StatusEnvelope struct {
	Data k6StatusData `json:"data"`
}

type k6StatusData struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Attributes model.K6Status `json:"attributes"`
}

type k6PatchEnvelope struct {
	Data k6PatchData `json:"data"`
}

type k6PatchData struct {
	Type       string              `json:"type"`
	ID         string              `json:"id"`
	Attributes model.K6StatusPatch `json:"attributes"`
}

// GetStatus retrieves the current k6 status.
func (w *Worker) GetStatus(ctx context.Context) (*model.K6Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.baseURL()+"/v1/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker %s: %w", w.Address, err)
	}
	defer resp.Body.Close()

	var envelope k6StatusEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("worker %s decode: %w", w.Address, err)
	}
	return &envelope.Data.Attributes, nil
}

// PatchStatus updates the k6 worker status (resume, pause, stop).
func (w *Worker) PatchStatus(ctx context.Context, patch model.K6StatusPatch) (*model.K6Status, error) {
	envelope := k6PatchEnvelope{
		Data: k6PatchData{
			Type:       "status",
			ID:         "default",
			Attributes: patch,
		},
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, w.baseURL()+"/v1/status", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker %s patch: %w", w.Address, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("worker %s patch status %d: %s", w.Address, resp.StatusCode, string(b))
	}

	var respEnvelope k6StatusEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&respEnvelope); err != nil {
		return nil, fmt.Errorf("worker %s decode: %w", w.Address, err)
	}
	return &respEnvelope.Data.Attributes, nil
}

// GetMetrics retrieves all metrics from the k6 worker.
func (w *Worker) GetMetrics(ctx context.Context) (map[string]model.K6Metric, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.baseURL()+"/v1/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker %s metrics: %w", w.Address, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("worker %s metrics status %d: %s", w.Address, resp.StatusCode, string(b))
	}

	// k6 returns metrics as a JSON:API array:
	// {"data": {"type": "metrics", "id": "...", "attributes": {...}}}
	// or as an array: {"data": [{"type": "metric", "id": "http_reqs", "attributes": {...}}, ...]}
	var raw json.RawMessage
	var outerWrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&outerWrapper); err != nil {
		return nil, fmt.Errorf("worker %s decode metrics outer: %w", w.Address, err)
	}
	raw = outerWrapper.Data

	// Try array format first (standard k6 response)
	type metricItem struct {
		Type       string         `json:"type"`
		ID         string         `json:"id"`
		Attributes model.K6Metric `json:"attributes"`
	}

	metrics := make(map[string]model.K6Metric)

	var items []metricItem
	if err := json.Unmarshal(raw, &items); err == nil {
		for _, item := range items {
			metrics[item.ID] = item.Attributes
		}
		return metrics, nil
	}

	// Fallback: try single object with nested metrics map
	var single struct {
		Metrics map[string]model.K6Metric `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &single); err == nil && single.Metrics != nil {
		return single.Metrics, nil
	}

	return metrics, nil
}

// IsReachable checks if the worker is reachable.
func (w *Worker) IsReachable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := w.GetStatus(ctx)
	return err == nil
}

// IsPaused checks if the worker is reachable AND in paused state,
// meaning the externally-controlled executor has fully initialized.
func (w *Worker) IsPaused(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := w.GetStatus(ctx)
	if err != nil {
		return false
	}
	return status.Paused
}
