package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shiva-load-testing/controller/internal/orchestrator"
)

func newLiveMetricsTestHandler() *TestHandler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	orch := orchestrator.New(
		[]string{"worker1:6565"},
		2*time.Second,
		2*time.Minute,
		logger,
		orchestrator.DashboardRuntimeConfig{},
	)
	return NewTestHandler(nil, orch, logger, "/scripts", "/output", nil, "http://controller:8080")
}

func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode json response: %v", err)
	}
	return payload
}

func TestGetLiveMetricsReturnsIdleWhenNoActiveTest(t *testing.T) {
	h := newLiveMetricsTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/metrics/live", nil)
	rec := httptest.NewRecorder()

	h.GetLiveMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	payload := decodeJSONBody(t, rec)
	if got := payload["status"]; got != "idle" {
		t.Fatalf("expected status idle, got %v", got)
	}
	if got := payload["phase"]; got != "idle" {
		t.Fatalf("expected phase idle, got %v", got)
	}
	if got := payload["message"]; got != "no test is currently running" {
		t.Fatalf("expected idle message, got %v", got)
	}
}

func TestGetLiveMetricsCollectingPhaseRemainsRunning(t *testing.T) {
	h := newLiveMetricsTestHandler()
	h.orch.SetPhase(orchestrator.PhaseCollecting, "Collecting final metrics and summary data...")

	req := httptest.NewRequest(http.MethodGet, "/api/metrics/live", nil)
	rec := httptest.NewRecorder()

	h.GetLiveMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	payload := decodeJSONBody(t, rec)
	if got := payload["status"]; got != "running" {
		t.Fatalf("expected status running, got %v", got)
	}
	if got := payload["phase"]; got != "collecting" {
		t.Fatalf("expected phase collecting, got %v", got)
	}
}

