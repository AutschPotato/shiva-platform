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
