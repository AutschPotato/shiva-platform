package completion

import "testing"

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
