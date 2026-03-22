package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestResumeAllForStartFailsAlreadyStartedNativeWorker(t *testing.T) {
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

	if err := orch.ResumeAllForStart(context.Background(), false); err == nil {
		t.Fatalf("expected native startup resume to fail when worker is already active")
	}
}

func TestResumeAllForStartFailsUnpausedControllableWorker(t *testing.T) {
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

	if err := orch.ResumeAllForStart(context.Background(), true); err == nil {
		t.Fatalf("expected controllable startup resume to fail when worker is already active")
	}
}

func TestResumeAllForStartRecoversFromUnpausedWorker(t *testing.T) {
	t.Helper()

	var recovered bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":2,"paused":true,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
		case http.MethodPatch:
			bodyBytes, _ := io.ReadAll(r.Body)
			body := string(bodyBytes)

			if strings.Contains(body, `"stopped":true`) {
				recovered = true
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":2,"paused":true,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
				return
			}

			if strings.Contains(body, `"paused":false`) && !recovered {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":[{"status":"500","title":"Pause error","detail":"test execution wasn't paused"}]}`))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":3,"paused":false,"vus":0,"vus-max":5,"stopped":false,"running":true,"tainted":false}}}`))
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
		t.Fatalf("expected startup recovery to succeed, got error: %v", err)
	}
	if !recovered {
		t.Fatalf("expected recovery path to stop and reload worker before retry")
	}
}

func TestResumeAllForStartRecoversAcrossMultipleRaces(t *testing.T) {
	t.Helper()

	stopCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":2,"paused":true,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
		case http.MethodPatch:
			bodyBytes, _ := io.ReadAll(r.Body)
			body := string(bodyBytes)

			if strings.Contains(body, `"stopped":true`) {
				stopCalls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":2,"paused":true,"vus":0,"vus-max":5,"stopped":false,"running":false,"tainted":false}}}`))
				return
			}

			if strings.Contains(body, `"paused":false`) && stopCalls < 2 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":[{"status":"500","title":"Pause error","detail":"test execution wasn't paused"}]}`))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"type":"status","id":"default","attributes":{"status":3,"paused":false,"vus":0,"vus-max":5,"stopped":false,"running":true,"tainted":false}}}`))
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
		t.Fatalf("expected startup recovery to succeed after repeated races, got error: %v", err)
	}
	if stopCalls < 2 {
		t.Fatalf("expected multiple recovery stop cycles, got %d", stopCalls)
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

func TestStartupReadyTimeoutForAttemptScalesWithWorkerCountAndAttempt(t *testing.T) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	small := New([]string{"worker1:6565"}, 2*time.Second, time.Hour, logger, DashboardRuntimeConfig{})
	large := New([]string{
		"worker1:6565", "worker2:6565", "worker3:6565", "worker4:6565", "worker5:6565",
		"worker6:6565", "worker7:6565", "worker8:6565", "worker9:6565", "worker10:6565",
	}, 2*time.Second, time.Hour, logger, DashboardRuntimeConfig{})

	smallAttempt1 := small.startupReadyTimeoutForAttempt(1)
	smallAttempt3 := small.startupReadyTimeoutForAttempt(3)
	largeAttempt1 := large.startupReadyTimeoutForAttempt(1)

	if smallAttempt1 < 25*time.Second {
		t.Fatalf("expected minimum adaptive timeout >= 25s, got %s", smallAttempt1)
	}
	if smallAttempt3 <= smallAttempt1 {
		t.Fatalf("expected later attempts to increase adaptive timeout, got attempt1=%s attempt3=%s", smallAttempt1, smallAttempt3)
	}
	if largeAttempt1 <= smallAttempt1 {
		t.Fatalf("expected larger worker fleets to increase adaptive timeout, got small=%s large=%s", smallAttempt1, largeAttempt1)
	}
}

func TestStartupReadyTimeoutForAttemptHonorsFixedOverride(t *testing.T) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	orch := New([]string{"worker1:6565", "worker2:6565"}, 2*time.Second, time.Hour, logger, DashboardRuntimeConfig{})

	orch.SetWorkerReadyTimeout(18 * time.Second)
	if got := orch.startupReadyTimeoutForAttempt(1); got != 18*time.Second {
		t.Fatalf("expected fixed worker ready timeout override to be used, got %s", got)
	}
	if got := orch.startupReadyTimeoutForAttempt(3); got != 18*time.Second {
		t.Fatalf("expected fixed worker ready timeout override to ignore attempt number, got %s", got)
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
