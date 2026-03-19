package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/orchestrator"
)

type DashboardHandler struct {
	orch   *orchestrator.Orchestrator
	logger *slog.Logger
}

func NewDashboardHandler(orch *orchestrator.Orchestrator, logger *slog.Logger) *DashboardHandler {
	return &DashboardHandler{
		orch:   orch,
		logger: logger,
	}
}

func (h *DashboardHandler) ListDashboards(w http.ResponseWriter, r *http.Request) {
	workers := h.orch.CheckWorkers(r.Context())
	activeTestID := h.orch.GetActiveTestID()
	phase, _ := h.orch.GetPhase()
	statuses := make([]model.WorkerDashboardStatus, 0, len(workers))
	for _, worker := range workers {
		statuses = append(statuses, buildWorkerDashboardStatus(worker, activeTestID, phase))
	}

	writeJSONResponse(w, http.StatusOK, map[string]any{
		"dashboards":  statuses,
		"active_test": activeTestID,
		"phase":       string(phase),
	})
}

func (h *DashboardHandler) ProxyDashboard(w http.ResponseWriter, r *http.Request) {
	workerName := chi.URLParam(r, "worker")
	if workerName == "" {
		writeDashboardUnavailableHTML(w, http.StatusBadRequest, "dashboard request is missing the worker name")
		return
	}

	worker := h.orch.FindWorker(workerName)
	if worker == nil {
		writeDashboardUnavailableHTML(w, http.StatusNotFound, fmt.Sprintf("worker %q is not configured", workerName))
		return
	}

	status, err := worker.GetStatus(r.Context())
	workerMetrics := model.WorkerMetrics{
		Name:             worker.Name(),
		Address:          worker.Address,
		DashboardEnabled: worker.DashboardEnabled,
		DashboardURL:     worker.DashboardURL(),
		Status:           "unreachable",
	}
	if err != nil {
		workerMetrics.Error = err.Error()
	} else {
		workerMetrics.Status = workerDashboardRuntimeStatus(status, h.orch.GetActiveTestID() != "")
		workerMetrics.VUs = status.VUs
	}

	phase, _ := h.orch.GetPhase()
	dashboardStatus := buildWorkerDashboardStatus(workerMetrics, h.orch.GetActiveTestID(), phase)
	if dashboardStatus.Availability != "available" {
		writeDashboardUnavailableHTML(w, http.StatusServiceUnavailable, dashboardStatus.Message)
		return
	}

	target, err := url.Parse(worker.DashboardURL())
	if err != nil {
		h.logger.Warn("dashboard proxy target parse failed", "worker", worker.Name(), "url", worker.DashboardURL(), "error", err)
		writeDashboardUnavailableHTML(w, http.StatusBadGateway, "dashboard target URL is invalid")
		return
	}

	proxyPath := chi.URLParam(r, "*")
	if proxyPath == "" {
		proxyPath = chi.URLParam(r, "proxyPath")
	}
	if proxyPath == "ui" && !strings.HasSuffix(r.URL.Path, "/") {
		target := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	if proxyPath == "" {
		proxyPath = "/"
	} else if !strings.HasPrefix(proxyPath, "/") {
		proxyPath = "/" + proxyPath
	}

	proxyReqCtx, cancelProxyReq := h.dashboardProxyRequestContext(r.Context(), proxyPath)
	defer cancelProxyReq()
	r = r.WithContext(proxyReqCtx)

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		req.URL.Path = joinProxyPath(target.Path, proxyPath)
		req.URL.RawPath = req.URL.EscapedPath()
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
		h.logger.Warn("dashboard proxy request failed", "worker", worker.Name(), "error", proxyErr)
		writeDashboardUnavailableHTML(rw, http.StatusBadGateway, "live worker dashboard is currently unreachable")
	}
	proxy.ServeHTTP(w, r)
}

func buildWorkerDashboardStatus(worker model.WorkerMetrics, activeTestID string, phase orchestrator.TestPhase) model.WorkerDashboardStatus {
	availability := "available"
	message := "live dashboard available while the active k6 run is alive"
	displayStatus := worker.Status

	if activeTestID != "" && worker.Status != "unreachable" {
		switch phase {
		case orchestrator.PhaseScript, orchestrator.PhaseWorkers:
			displayStatus = "preparing"
		case orchestrator.PhaseRunning:
			displayStatus = "running"
		case orchestrator.PhaseCollecting:
			displayStatus = "collecting"
		}
	}

	switch {
	case !worker.DashboardEnabled:
		availability = "disabled"
		message = "k6 internal dashboards are disabled in the current runtime configuration"
	case worker.Status == "unreachable":
		availability = "worker_unreachable"
		if worker.Error != "" {
			message = worker.Error
		} else {
			message = "worker is currently unreachable"
		}
	case activeTestID == "":
		availability = "not_running"
		message = "no active test run is present, so the live k6 dashboard is not expected to be available"
	}

	return model.WorkerDashboardStatus{
		Name:             worker.Name,
		Address:          worker.Address,
		WorkerStatus:     displayStatus,
		Phase:            string(phase),
		DashboardEnabled: worker.DashboardEnabled,
		DashboardURL:     worker.DashboardURL,
		Availability:     availability,
		Message:          message,
		ActiveTestID:     activeTestID,
	}
}

func shouldKeepDashboardStreamOpen(activeTestID string, phase orchestrator.TestPhase) bool {
	if activeTestID == "" {
		return false
	}
	switch phase {
	case orchestrator.PhaseCollecting, orchestrator.PhaseDone, orchestrator.PhaseError:
		return false
	default:
		return true
	}
}

func (h *DashboardHandler) dashboardProxyRequestContext(parent context.Context, proxyPath string) (context.Context, context.CancelFunc) {
	if !strings.HasSuffix(proxyPath, "/events") && proxyPath != "/events" {
		return parent, func() {}
	}

	ctx, cancel := context.WithCancel(parent)
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				phase, _ := h.orch.GetPhase()
				activeTestID := h.orch.GetActiveTestID()
				if !shouldKeepDashboardStreamOpen(activeTestID, phase) {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func workerDashboardRuntimeStatus(status *model.K6Status, testActive bool) string {
	if status == nil {
		return "unreachable"
	}
	if status.IsRunning() && !status.Paused {
		return "running"
	}
	if status.Paused {
		return "paused"
	}
	if status.IsFinished() {
		if testActive {
			return "done"
		}
		return "online"
	}
	return "online"
}

func joinProxyPath(basePath string, subPath string) string {
	switch {
	case basePath == "" || basePath == "/":
		return subPath
	case subPath == "" || subPath == "/":
		return basePath
	case strings.HasSuffix(basePath, "/") && strings.HasPrefix(subPath, "/"):
		return basePath + strings.TrimPrefix(subPath, "/")
	case strings.HasSuffix(basePath, "/") || strings.HasPrefix(subPath, "/"):
		return basePath + subPath
	default:
		return basePath + "/" + subPath
	}
}

func writeDashboardUnavailableHTML(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>Worker Dashboard Unavailable</title>
    <style>
      body { font-family: system-ui, sans-serif; background: #120912; color: #f7eef5; padding: 32px; }
      .panel { max-width: 720px; margin: 0 auto; background: #201320; border: 1px solid #4b2a42; border-radius: 16px; padding: 24px; }
      h1 { margin-top: 0; color: #ff4da6; }
      p { line-height: 1.5; color: #e8d8e6; }
      code { background: #2b1830; padding: 2px 6px; border-radius: 6px; }
    </style>
  </head>
  <body>
    <div class="panel">
      <h1>Worker Dashboard Unavailable</h1>
      <p>%s</p>
      <p>This view is live-only and is expected to disappear when there is no active k6 run on the selected worker.</p>
    </div>
  </body>
</html>`, html.EscapeString(message))
}

func writeJSONResponse(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
