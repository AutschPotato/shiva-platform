package orchestrator

import (
	"context"
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
