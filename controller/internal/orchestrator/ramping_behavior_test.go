package orchestrator

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
)

func TestShouldCompleteManagedRampWhenWorkersEndedAfterRunStarted(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":7,"paused":false,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers:      []*Worker{worker},
		logger:       slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		controllable: true,
	}

	state := pollStateSnapshot{
		seenRunning:    true,
		controllable:   true,
		hasManagedRamp: true,
		rampingDone:    false,
		zeroMetricRun:  2,
		totalVUs:       0,
	}

	if !orch.shouldCompleteTest(context.Background(), 5, state) {
		t.Fatalf("expected managed-ramp run to complete once workers have ended after run start")
	}
}

func TestShouldNotCompleteManagedRampBeforeRunStarts(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":7,"paused":false,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers:      []*Worker{worker},
		logger:       slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		controllable: true,
	}

	state := pollStateSnapshot{
		seenRunning:    false,
		controllable:   true,
		hasManagedRamp: true,
		rampingDone:    false,
		zeroMetricRun:  2,
		totalVUs:       0,
	}

	if orch.shouldCompleteTest(context.Background(), 10, state) {
		t.Fatalf("expected managed-ramp run to stay active before workers ever reported running")
	}
}

func TestShouldNotCompleteManagedRampWhileVUsStillReported(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":7,"paused":false,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers:      []*Worker{worker},
		logger:       slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		controllable: true,
	}

	state := pollStateSnapshot{
		seenRunning:    true,
		controllable:   true,
		hasManagedRamp: true,
		rampingDone:    false,
		zeroMetricRun:  0,
		totalVUs:       10,
	}

	if orch.shouldCompleteTest(context.Background(), 5, state) {
		t.Fatalf("expected managed-ramp run to stay active while metrics still report active VUs")
	}
}

func TestRampingManagerRetriesScaleErrorsUntilStagesComplete(t *testing.T) {
	t.Helper()

	var patchAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" || r.Method != http.MethodPatch {
			http.NotFound(w, r)
			return
		}

		attempt := patchAttempts.Add(1)
		if attempt <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"status":"500","detail":"transient scale error"}]}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":4,"paused":false,"vus":1,"vus-max":5,"stopped":false,"running":true,"tainted":false}}}`))
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers: []*Worker{worker},
		logger:  slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}

	rm := NewRampingManager(orch, orch.logger)
	startedAt := time.Now()
	rm.Start([]model.Stage{
		{Duration: "1s", Target: 1},
		{Duration: "4s", Target: 1},
	})
	defer rm.Stop()

	select {
	case <-rm.Done():
	case <-time.After(7 * time.Second):
		t.Fatalf("expected ramping manager to finish stages within timeout")
	}

	if patchAttempts.Load() < 3 {
		t.Fatalf("expected ramping manager to retry scale failures, got %d patch attempts", patchAttempts.Load())
	}
	if time.Since(startedAt) < 3*time.Second {
		t.Fatalf("expected ramping manager to keep running after transient failures")
	}

	orch.mu.RLock()
	rampDone := orch.rampingDone
	orch.mu.RUnlock()
	if !rampDone {
		t.Fatalf("expected ramping manager to mark rampingDone after stages completed")
	}
}
