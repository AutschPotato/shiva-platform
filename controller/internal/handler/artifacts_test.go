package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/shiva-load-testing/controller/internal/completion"
)

func TestUploadArtifactStoresSummaryInRegistry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := completion.NewRegistry()
	registry.RegisterRun("run-1", []string{"worker1"}, "token-1")

	handler := NewTestHandler(nil, nil, logger, t.TempDir(), t.TempDir(), registry, "http://controller:8080")
	req := httptest.NewRequest(http.MethodPost, "/api/internal/runs/run-1/workers/worker1/summary", strings.NewReader(`{"ok":true}`))
	req.Header.Set("X-Shiva-Artifact-Token", "token-1")
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("testID", "run-1")
	routeCtx.URLParams.Add("workerID", "worker1")
	routeCtx.URLParams.Add("artifactType", "summary")
	req = req.WithContext(contextWithRoute(req, routeCtx))

	rec := httptest.NewRecorder()
	handler.UploadArtifact(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	snapshot, ok := registry.Snapshot("run-1")
	if !ok {
		t.Fatalf("expected run snapshot")
	}
	if got := snapshot.Artifacts[completion.ArtifactSummary]["worker1"].Content; got != `{"ok":true}` {
		t.Fatalf("expected uploaded summary to be stored, got %q", got)
	}
}

func TestUploadArtifactRejectsUnknownWorker(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := completion.NewRegistry()
	registry.RegisterRun("run-1", []string{"worker1"}, "token-1")

	handler := NewTestHandler(nil, nil, logger, t.TempDir(), t.TempDir(), registry, "http://controller:8080")
	req := httptest.NewRequest(http.MethodPost, "/api/internal/runs/run-1/workers/worker2/summary", strings.NewReader(`{"ok":true}`))
	req.Header.Set("X-Shiva-Artifact-Token", "token-1")
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("testID", "run-1")
	routeCtx.URLParams.Add("workerID", "worker2")
	routeCtx.URLParams.Add("artifactType", "summary")
	req = req.WithContext(contextWithRoute(req, routeCtx))

	rec := httptest.NewRecorder()
	handler.UploadArtifact(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func contextWithRoute(req *http.Request, routeCtx *chi.Context) context.Context {
	return context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
}
