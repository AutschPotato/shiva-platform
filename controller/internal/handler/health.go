package handler

import (
	"encoding/json"
	"net/http"

	"github.com/shiva-load-testing/controller/internal/orchestrator"
)

type HealthHandler struct {
	orch *orchestrator.Orchestrator
}

func NewHealthHandler(orch *orchestrator.Orchestrator) *HealthHandler {
	return &HealthHandler{orch: orch}
}

func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	workers := h.orch.CheckWorkers(r.Context())
	resp := map[string]interface{}{
		"status":  "ok",
		"workers": workers,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
