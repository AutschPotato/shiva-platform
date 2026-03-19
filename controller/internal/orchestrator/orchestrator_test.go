package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShouldCompleteNativeRunWhenWorkerStatusShowsEnded(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":7,"paused":true,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers: []*Worker{worker},
		logger:  slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}

	state := pollStateSnapshot{
		seenRunning:    true,
		controllable:   false,
		hasManagedRamp: false,
		zeroMetricRun:  0,
		rampingDone:    false,
	}

	if !orch.shouldCompleteTest(context.Background(), 5, state) {
		t.Fatalf("expected native run to complete when worker status already reports paused/finished")
	}
}

func TestResumeAllForStartToleratesAlreadyStartedNativeWorker(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/status":
			if r.Method != http.MethodPatch {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"status":"500","title":"Pause error","detail":"constant-arrival-rate executor 'default' doesn't support pause and resume operations after its start"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers: []*Worker{worker},
		logger:  slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}

	if err := orch.ResumeAllForStart(context.Background(), false); err != nil {
		t.Fatalf("expected native startup resume to tolerate already-started worker: %v", err)
	}
}

func TestResumeAllForStartToleratesUnpausedControllableWorker(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/status":
			if r.Method != http.MethodPatch {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"status":"500","title":"Pause error","detail":"test execution wasn't paused"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers: []*Worker{worker},
		logger:  slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}

	if err := orch.ResumeAllForStart(context.Background(), true); err != nil {
		t.Fatalf("expected controllable startup resume to tolerate already-active worker: %v", err)
	}
}

func TestWaitForAllReadyRequiresStableReadyState(t *testing.T) {
	t.Helper()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		calls++
		statusCode := 2
		paused := true
		stopped := false
		if calls == 1 {
			statusCode = 7
		}
		resp := map[string]any{
			"data": map[string]any{
				"type": "status",
				"id":   "default",
				"attributes": map[string]any{
					"status":  statusCode,
					"paused":  paused,
					"vus":     0,
					"vus-max": 1,
					"stopped": stopped,
					"running": false,
					"tainted": false,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second
	orch := &Orchestrator{
		workers: []*Worker{worker},
		logger:  slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := orch.WaitForAllReady(ctx); err != nil {
		t.Fatalf("expected worker to become stably ready: %v", err)
	}
	if calls < 3 {
		t.Fatalf("expected multiple readiness checks, got %d", calls)
	}
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (n int, err error) {
	w.t.Helper()
	return len(p), nil
}
func TestShouldNotCompleteNativeRunWhenWorkerStatusIsUnreachable(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	addr := server.Listener.Addr().String()
	server.Close()

	worker := NewWorker(addr, false, "", 0)
	worker.client.Timeout = 200 * time.Millisecond

	orch := &Orchestrator{
		workers: []*Worker{worker},
		logger:  slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}

	state := pollStateSnapshot{
		seenRunning:    true,
		controllable:   false,
		hasManagedRamp: false,
		zeroMetricRun:  0,
		rampingDone:    false,
	}

	if orch.shouldCompleteTest(context.Background(), 5, state) {
		t.Fatalf("expected native run to stay active when a worker status check fails")
	}
}
func TestShouldNotCompleteNativeRunBeforeExpectedDurationWhenWorkerStatusShowsEnded(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":7,"paused":true,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers:             []*Worker{worker},
		logger:              slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		pollInterval:        2 * time.Second,
		testStartTime:       time.Now(),
		expectedRunDuration: time.Minute,
	}

	state := pollStateSnapshot{
		seenRunning:         true,
		controllable:        false,
		hasManagedRamp:      false,
		zeroMetricRun:       0,
		rampingDone:         false,
		expectedRunDuration: time.Minute,
	}

	if orch.shouldCompleteTest(context.Background(), 5, state) {
		t.Fatalf("expected native run to stay active before its configured duration elapsed")
	}
}

func TestShouldCompleteNativeRunAfterExpectedDurationWhenWorkerStatusShowsEnded(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":7,"paused":true,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
	}))
	defer server.Close()

	worker := NewWorker(server.Listener.Addr().String(), false, "", 0)
	worker.client.Timeout = 2 * time.Second

	orch := &Orchestrator{
		workers:             []*Worker{worker},
		logger:              slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		pollInterval:        2 * time.Second,
		testStartTime:       time.Now().Add(-59 * time.Second),
		expectedRunDuration: time.Minute,
	}

	state := pollStateSnapshot{
		seenRunning:         true,
		controllable:        false,
		hasManagedRamp:      false,
		zeroMetricRun:       0,
		rampingDone:         false,
		expectedRunDuration: time.Minute,
	}

	if !orch.shouldCompleteTest(context.Background(), 30, state) {
		t.Fatalf("expected native run to complete once its configured duration effectively elapsed")
	}
}
