package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/shiva-load-testing/controller/internal/completion"
)

func (h *TestHandler) UploadArtifact(w http.ResponseWriter, r *http.Request) {
	if h.completionRegistry == nil {
		httpError(w, "artifact uploads are not configured", http.StatusServiceUnavailable)
		return
	}

	testID := chi.URLParam(r, "testID")
	workerID := strings.TrimSpace(chi.URLParam(r, "workerID"))
	artifactType := completion.ArtifactType(strings.TrimSpace(chi.URLParam(r, "artifactType")))
	uploadToken := strings.TrimSpace(r.Header.Get("X-Shiva-Artifact-Token"))

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		httpError(w, "invalid artifact upload body", http.StatusBadRequest)
		return
	}

	if err := h.completionRegistry.StoreArtifact(testID, workerID, uploadToken, artifactType, r.Header.Get("Content-Type"), body); err != nil {
		h.logger.Warn("artifact upload rejected",
			"test_id", testID,
			"worker_id", workerID,
			"artifact_type", artifactType,
			"error", err,
		)
		switch {
		case errors.Is(err, completion.ErrUnknownRun):
			httpError(w, "unknown test run", http.StatusNotFound)
		case errors.Is(err, completion.ErrUnauthorized):
			httpError(w, "unauthorized artifact upload", http.StatusForbidden)
		case errors.Is(err, completion.ErrUnknownWorker), errors.Is(err, completion.ErrUnknownArtifact):
			httpError(w, err.Error(), http.StatusBadRequest)
		default:
			h.logger.Error("artifact upload failed", "test_id", testID, "worker_id", workerID, "artifact_type", artifactType, "error", err)
			httpError(w, "failed to store artifact", http.StatusInternalServerError)
		}
		return
	}

	h.logger.Info("artifact uploaded",
		"test_id", testID,
		"worker_id", workerID,
		"artifact_type", artifactType,
		"size_bytes", len(body),
	)

	if err := h.enrichCompletedResultFromRegistry(r.Context(), testID); err != nil {
		h.logger.Warn("late artifact enrichment failed",
			"test_id", testID,
			"worker_id", workerID,
			"artifact_type", artifactType,
			"error", err,
		)
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":        "accepted",
		"test_id":       testID,
		"worker_id":     workerID,
		"artifact_type": string(artifactType),
	})
}
