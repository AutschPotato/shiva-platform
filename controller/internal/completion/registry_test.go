package completion

import (
	"testing"
	"time"
)

func TestRegistryStoresArtifactsIdempotently(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterRun("run-1", []string{"worker1", "worker2"}, "token-1")

	if err := registry.StoreArtifact("run-1", "worker1", "token-1", ArtifactSummary, "application/json", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("first upload failed: %v", err)
	}
	if err := registry.StoreArtifact("run-1", "worker1", "token-1", ArtifactSummary, "application/json", []byte(`{"ok":true,"retry":1}`)); err != nil {
		t.Fatalf("retry upload failed: %v", err)
	}

	snapshot, ok := registry.Snapshot("run-1")
	if !ok {
		t.Fatalf("expected snapshot to exist")
	}
	raw := BuildRawSummary(snapshot, ArtifactSummary)
	if raw == "" {
		t.Fatalf("expected raw summary content")
	}
	if got := snapshot.Artifacts[ArtifactSummary]["worker1"].Content; got != `{"ok":true,"retry":1}` {
		t.Fatalf("expected latest artifact content to win, got %q", got)
	}
}

func TestRegistryRejectsUnknownWorkerAndBadToken(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterRun("run-1", []string{"worker1"}, "token-1")

	if err := registry.StoreArtifact("run-1", "worker1", "wrong-token", ArtifactSummary, "application/json", []byte(`{}`)); err != ErrUnauthorized {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
	if err := registry.StoreArtifact("run-1", "worker2", "token-1", ArtifactSummary, "application/json", []byte(`{}`)); err == nil {
		t.Fatalf("expected unknown worker error")
	}
}

func TestRegistryRetainsClosedRunsWithinLateUploadTTL(t *testing.T) {
	registry := newRegistryWithTTL(50 * time.Millisecond)
	registry.RegisterRun("run-1", []string{"worker1"}, "token-1")
	registry.RemoveRun("run-1")

	if err := registry.StoreArtifact("run-1", "worker1", "token-1", ArtifactSummary, "application/json", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("expected late upload to be accepted before ttl expiry, got %v", err)
	}

	if _, ok := registry.Snapshot("run-1"); !ok {
		t.Fatalf("expected closed run snapshot to remain available during ttl")
	}

	time.Sleep(80 * time.Millisecond)

	if err := registry.StoreArtifact("run-1", "worker1", "token-1", ArtifactSummary, "application/json", []byte(`{"late":true}`)); err != ErrUnknownRun {
		t.Fatalf("expected run to expire after ttl, got %v", err)
	}
}
