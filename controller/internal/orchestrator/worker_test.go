package orchestrator

import "testing"

func TestWorkerDashboardURLUsesWorkerHostForWildcardBinding(t *testing.T) {
	worker := NewWorker("worker3:6565", true, "0.0.0.0", 5665)

	if got, want := worker.Name(), "worker3"; got != want {
		t.Fatalf("worker name mismatch: got %q want %q", got, want)
	}
	if got, want := worker.DashboardURL(), "http://worker3:5665"; got != want {
		t.Fatalf("dashboard url mismatch: got %q want %q", got, want)
	}
}

func TestWorkerDashboardURLDisabledWhenRuntimeOff(t *testing.T) {
	worker := NewWorker("worker7:6565", false, "0.0.0.0", 5665)

	if got := worker.DashboardURL(); got != "" {
		t.Fatalf("expected empty dashboard url when disabled, got %q", got)
	}
}
