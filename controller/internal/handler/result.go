package handler

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/shiva-load-testing/controller/internal/middleware"
	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/store"
)

type ResultHandler struct {
	store  *store.Store
	logger *slog.Logger
}

type resultListItem struct {
	ID            string      `json:"id"`
	ProjectName   string      `json:"project_name"`
	URL           string      `json:"url"`
	Status        string      `json:"status"`
	UserID        int64       `json:"user_id"`
	Username      string      `json:"username"`
	CreatedAt     interface{} `json:"created_at"`
	ErrorRate     *float64    `json:"error_rate,omitempty"`
	TotalRequests *float64    `json:"total_requests,omitempty"`
	AvgLatency    *float64    `json:"avg_latency_ms,omitempty"`
	P95Latency    *float64    `json:"p95_latency_ms,omitempty"`
	DurationS     *float64    `json:"duration_s,omitempty"`
	TotalVUs      *int        `json:"total_vus,omitempty"`
	RunBy         interface{} `json:"run_by"`
}

func NewResultHandler(s *store.Store, logger *slog.Logger) *ResultHandler {
	return &ResultHandler{store: s, logger: logger}
}

func parseResultListParams(r *http.Request) (int, int, string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset, r.URL.Query().Get("search")
}

func canViewResult(role string, userID int64, lt *model.LoadTest) bool {
	return role == "admin" || lt.UserID == userID
}

func clampLatencyValue(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

func normalizeResultMetrics(metrics *model.AggregatedMetrics) {
	if metrics == nil {
		return
	}

	metrics.AvgLatency = clampLatencyValue(metrics.AvgLatency)
	metrics.MedLatency = clampLatencyValue(metrics.MedLatency)
	metrics.P90Latency = clampLatencyValue(metrics.P90Latency)
	metrics.P95Latency = clampLatencyValue(metrics.P95Latency)
	metrics.P99Latency = clampLatencyValue(metrics.P99Latency)
	metrics.MinLatency = clampLatencyValue(metrics.MinLatency)
	metrics.MaxLatency = clampLatencyValue(metrics.MaxLatency)

	for i := range metrics.Workers {
		metrics.Workers[i].AvgLatency = clampLatencyValue(metrics.Workers[i].AvgLatency)
	}
}

func buildResultListItem(lt model.LoadTest) resultListItem {
	item := resultListItem{
		ID:          lt.ID,
		ProjectName: lt.ProjectName,
		URL:         lt.URL,
		Status:      lt.Status,
		UserID:      lt.UserID,
		Username:    lt.Username,
		CreatedAt:   lt.CreatedAt,
		RunBy:       map[string]interface{}{"username": lt.Username, "id": lt.UserID},
	}

	if len(lt.ResultJSON) > 0 {
		var tr model.TestResult
		if err := json.Unmarshal(lt.ResultJSON, &tr); err == nil {
			tr.Metadata = hydrateAuthMetadataFromRawSummary(tr.Metadata, tr.AuthSummaryContent)
			tr.MetricsV2 = buildMetricsV2(tr.Metrics, tr.SummaryContent, tr.Metadata)
			if tr.Metrics != nil {
				normalizeResultMetrics(tr.Metrics)
				item.ErrorRate = &tr.Metrics.ErrorRate
				item.TotalRequests = &tr.Metrics.TotalRequests
				item.AvgLatency = &tr.Metrics.AvgLatency
				item.P95Latency = &tr.Metrics.P95Latency
				item.TotalVUs = &tr.Metrics.TotalVUs
			} else if tr.MetricsV2 != nil {
				errorRate := tr.MetricsV2.HTTPBusiness.ErrorRate
				totalRequests := tr.MetricsV2.HTTPTotal.Requests
				avgLatency := tr.MetricsV2.PrimaryLatency.AvgMs
				p95Latency := tr.MetricsV2.PrimaryLatency.P95Ms
				item.ErrorRate = &errorRate
				item.TotalRequests = &totalRequests
				item.AvgLatency = &avgLatency
				item.P95Latency = &p95Latency
			}
			if tr.Metadata != nil {
				item.DurationS = &tr.Metadata.DurationS
			}
		}
	}

	return item
}

func buildResultResponse(lt *model.LoadTest) map[string]interface{} {
	resp := map[string]interface{}{
		"id":           lt.ID,
		"project_name": lt.ProjectName,
		"url":          lt.URL,
		"status":       lt.Status,
		"user_id":      lt.UserID,
		"username":     lt.Username,
		"created_at":   lt.CreatedAt,
		"run_by":       map[string]interface{}{"username": lt.Username, "id": lt.UserID},
	}

	if len(lt.ResultJSON) > 0 {
		var result model.TestResult
		if err := json.Unmarshal(lt.ResultJSON, &result); err == nil {
			result.Metadata = hydrateAuthMetadataFromRawSummary(result.Metadata, result.AuthSummaryContent)
			result.MetricsV2 = buildMetricsV2(result.Metrics, result.SummaryContent, result.Metadata)
			if result.Metrics != nil {
				normalizeResultMetrics(result.Metrics)
				resp["metrics"] = result.Metrics
			}
			if result.MetricsV2 != nil {
				resp["metrics_v2"] = result.MetricsV2
			}
			if len(result.TimeSeries) > 0 {
				resp["time_series"] = result.TimeSeries
			}
			if result.Metadata != nil {
				resp["metadata"] = result.Metadata
			}
			if len(result.Warnings) > 0 {
				resp["warnings"] = result.Warnings
			}
			if result.SummaryContent != "" {
				resp["summary_content"] = result.SummaryContent
			}
			if result.AuthSummaryContent != "" {
				resp["auth_summary_content"] = result.AuthSummaryContent
			}
		}
	}

	if lt.ScriptContent != "" {
		resp["script_content"] = lt.ScriptContent
	}
	if lt.ConfigContent != "" {
		resp["config_content"] = lt.ConfigContent
	}
	if lt.PayloadSourceJSON != "" {
		resp["payload_source_json"] = lt.PayloadSourceJSON
	}
	if lt.PayloadContent != "" {
		resp["payload_content"] = lt.PayloadContent
	}
	if lt.HTTPMethod != "" {
		resp["http_method"] = lt.HTTPMethod
	}
	if lt.ContentType != "" {
		resp["content_type"] = lt.ContentType
	}

	return resp
}

func (h *ResultHandler) ListResults(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	role := middleware.GetRole(r.Context())

	limit, offset, search := parseResultListParams(r)

	results, total, err := h.store.ListLoadTests(r.Context(), userID, role, limit, offset, search)
	if err != nil {
		h.logger.Error("list results failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	items := make([]resultListItem, len(results))
	for i, result := range results {
		items[i] = buildResultListItem(result)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results": items,
		"total":   total,
	})
}

func (h *ResultHandler) GetResult(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		httpError(w, "missing id", http.StatusBadRequest)
		return
	}

	userID := middleware.GetUserID(r.Context())
	role := middleware.GetRole(r.Context())

	lt, err := h.store.GetLoadTest(r.Context(), id)
	if err != nil {
		h.logger.Error("get result failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if lt == nil {
		httpError(w, "not found", http.StatusNotFound)
		return
	}

	if !canViewResult(role, userID, lt) {
		httpError(w, "not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, buildResultResponse(lt))
}

func (h *ResultHandler) ResetData(w http.ResponseWriter, r *http.Request) {
	if err := h.store.ResetData(r.Context()); err != nil {
		h.logger.Error("reset data failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "all test data deleted"})
}
