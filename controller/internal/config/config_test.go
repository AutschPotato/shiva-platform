package config

import "testing"

func TestEnvBool(t *testing.T) {
	t.Setenv("TEST_BOOL_TRUE", "true")
	t.Setenv("TEST_BOOL_FALSE", "off")
	t.Setenv("TEST_BOOL_INVALID", "maybe")

	if got := envBool("TEST_BOOL_TRUE", false); !got {
		t.Fatalf("expected true for TEST_BOOL_TRUE")
	}
	if got := envBool("TEST_BOOL_FALSE", true); got {
		t.Fatalf("expected false for TEST_BOOL_FALSE")
	}
	if got := envBool("TEST_BOOL_INVALID", true); !got {
		t.Fatalf("expected fallback true for TEST_BOOL_INVALID")
	}
	if got := envBool("TEST_BOOL_MISSING", false); got {
		t.Fatalf("expected fallback false for missing var")
	}
}

func TestLoadParsesWorkerReadyTimeoutOverride(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("K6_WORKERS", "worker1:6565")
	t.Setenv("K6_WORKER_READY_TIMEOUT_SEC", "75")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected load to succeed, got error: %v", err)
	}
	if cfg.K6WorkerReadyTimeoutSec != 75 {
		t.Fatalf("expected K6WorkerReadyTimeoutSec=75, got %d", cfg.K6WorkerReadyTimeoutSec)
	}
}

func TestLoadDefaultsWorkerReadyTimeoutToAdaptiveMode(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("K6_WORKERS", "worker1:6565")
	t.Setenv("K6_WORKER_READY_TIMEOUT_SEC", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected load to succeed, got error: %v", err)
	}
	if cfg.K6WorkerReadyTimeoutSec != 0 {
		t.Fatalf("expected K6WorkerReadyTimeoutSec default 0, got %d", cfg.K6WorkerReadyTimeoutSec)
	}
}
