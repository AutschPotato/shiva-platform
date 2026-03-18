package handler

import (
	"testing"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/orchestrator"
)

func TestBuildWorkerDashboardStatusAvailability(t *testing.T) {
	worker := model.WorkerMetrics{
		Name:             "worker1",
		Address:          "worker1:6565",
		Status:           "running",
		DashboardEnabled: true,
		DashboardURL:     "http://worker1:5665",
	}

	got := buildWorkerDashboardStatus(worker, "test-123", orchestrator.PhaseRunning)
	if got.Availability != "available" {
		t.Fatalf("expected available, got %q", got.Availability)
	}
	if got.WorkerStatus != "running" {
		t.Fatalf("expected running display status during active test, got %q", got.WorkerStatus)
	}

	got = buildWorkerDashboardStatus(worker, "", orchestrator.PhaseDone)
	if got.Availability != "not_running" {
		t.Fatalf("expected not_running without active test, got %q", got.Availability)
	}

	worker.DashboardEnabled = false
	got = buildWorkerDashboardStatus(worker, "test-123", orchestrator.PhaseRunning)
	if got.Availability != "disabled" {
		t.Fatalf("expected disabled, got %q", got.Availability)
	}

	worker.DashboardEnabled = true
	worker.Status = "unreachable"
	worker.Error = "dial tcp timeout"
	got = buildWorkerDashboardStatus(worker, "test-123", orchestrator.PhaseRunning)
	if got.Availability != "worker_unreachable" {
		t.Fatalf("expected worker_unreachable, got %q", got.Availability)
	}
	if got.Message != "dial tcp timeout" {
		t.Fatalf("expected worker error message to be preserved, got %q", got.Message)
	}
}

func TestBuildWorkerDashboardStatusMapsPhaseToDisplayStatus(t *testing.T) {
	worker := model.WorkerMetrics{
		Name:             "worker1",
		Address:          "worker1:6565",
		Status:           "done",
		DashboardEnabled: true,
		DashboardURL:     "http://worker1:5665",
	}

	tests := []struct {
		name      string
		phase     string
		wantState string
	}{
		{name: "script", phase: "script", wantState: "preparing"},
		{name: "workers", phase: "workers", wantState: "preparing"},
		{name: "running", phase: "running", wantState: "running"},
		{name: "collecting", phase: "collecting", wantState: "collecting"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildWorkerDashboardStatus(worker, "test-123", orchestrator.TestPhase(tc.phase))
			if got.WorkerStatus != tc.wantState {
				t.Fatalf("expected %q, got %q", tc.wantState, got.WorkerStatus)
			}
		})
	}
}

func TestJoinProxyPath(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		sub     string
		wantOut string
	}{
		{name: "root base", base: "", sub: "/", wantOut: "/"},
		{name: "plain subpath", base: "", sub: "/assets/app.js", wantOut: "/assets/app.js"},
		{name: "base slash", base: "/dashboard/", sub: "/assets/app.js", wantOut: "/dashboard/assets/app.js"},
		{name: "base no slash", base: "/dashboard", sub: "/assets/app.js", wantOut: "/dashboard/assets/app.js"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinProxyPath(tc.base, tc.sub); got != tc.wantOut {
				t.Fatalf("joinProxyPath(%q, %q) = %q, want %q", tc.base, tc.sub, got, tc.wantOut)
			}
		})
	}
}

func TestShouldKeepDashboardStreamOpen(t *testing.T) {
	tests := []struct {
		name       string
		activeTest string
		phase      orchestrator.TestPhase
		want       bool
	}{
		{name: "running", activeTest: "test-123", phase: orchestrator.PhaseRunning, want: true},
		{name: "workers", activeTest: "test-123", phase: orchestrator.PhaseWorkers, want: true},
		{name: "collecting", activeTest: "test-123", phase: orchestrator.PhaseCollecting, want: false},
		{name: "done", activeTest: "test-123", phase: orchestrator.PhaseDone, want: false},
		{name: "error", activeTest: "test-123", phase: orchestrator.PhaseError, want: false},
		{name: "no active test", activeTest: "", phase: orchestrator.PhaseRunning, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldKeepDashboardStreamOpen(tc.activeTest, tc.phase)
			if got != tc.want {
				t.Fatalf("shouldKeepDashboardStreamOpen(%q, %q) = %v, want %v", tc.activeTest, tc.phase, got, tc.want)
			}
		})
	}
}
