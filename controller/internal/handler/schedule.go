package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/shiva-load-testing/controller/internal/middleware"
	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scheduler"
	"github.com/shiva-load-testing/controller/internal/secrets"
	"github.com/shiva-load-testing/controller/internal/store"
)

type scheduleOverlapChecker func(context.Context, *store.Store, scheduler.TimeSlot, string, *scheduler.RunningTestInfo) (*model.ScheduleConflict, error)

type ScheduleHandler struct {
	store          *store.Store
	sched          *scheduler.Scheduler
	active         scheduler.ActiveTestProvider
	logger         *slog.Logger
	secretSvc      *secrets.Service
	overlapChecker scheduleOverlapChecker
}

type validatedScheduleCreate struct {
	scheduledAt time.Time
	timezone    string
	recType     string
	recEnd      *time.Time
	durationS   int
}

type scheduleDeleteScope string

const (
	deleteScopeSingle scheduleDeleteScope = "single"
	deleteScopeFuture scheduleDeleteScope = "future"
)

func NewScheduleHandler(s *store.Store, sched *scheduler.Scheduler, active scheduler.ActiveTestProvider, logger *slog.Logger, encryptionKey string) *ScheduleHandler {
	var secretSvc *secrets.Service
	if encryptionKey != "" {
		if svc, err := secrets.NewService(encryptionKey); err == nil {
			secretSvc = svc
		}
	}
	return &ScheduleHandler{
		store:          s,
		sched:          sched,
		active:         active,
		logger:         logger,
		secretSvc:      secretSvc,
		overlapChecker: scheduler.CheckOverlap,
	}
}

func (h *ScheduleHandler) activeRunningTest() *scheduler.RunningTestInfo {
	if h.active == nil {
		return nil
	}
	if activeID := h.active.GetActiveTestID(); activeID != "" {
		return &scheduler.RunningTestInfo{
			TestID:    activeID,
			StartTime: h.active.GetTestStartTime(),
		}
	}
	return nil
}

func (h *ScheduleHandler) loadSchedule(r *http.Request, id string) (*model.ScheduledTest, error) {
	return h.store.GetSchedule(r.Context(), id)
}

func (h *ScheduleHandler) managedSchedule(w http.ResponseWriter, r *http.Request, id string) (*model.ScheduledTest, bool) {
	existing, err := h.loadSchedule(r, id)
	if err != nil || existing == nil {
		httpError(w, "schedule not found", http.StatusNotFound)
		return nil, false
	}
	if !h.canManage(r, existing) {
		httpError(w, "forbidden", http.StatusForbidden)
		return nil, false
	}
	return existing, true
}

func (h *ScheduleHandler) validateCreateRequest(req *model.CreateScheduleRequest) (*validatedScheduleCreate, string, int) {
	if req.Name == "" {
		return nil, "name is required", http.StatusBadRequest
	}
	if req.ProjectName == "" {
		return nil, "project_name is required", http.StatusBadRequest
	}
	if req.ScheduledAt == "" {
		return nil, "scheduled_at is required", http.StatusBadRequest
	}

	hasScript := req.ScriptContent != ""
	hasBuilder := req.URL != ""
	if !hasScript && !hasBuilder {
		return nil, "provide either script_content or url (builder)", http.StatusBadRequest
	}
	if err := normalizeScheduleRequestPayload(req); err != nil {
		return nil, err.Error(), http.StatusBadRequest
	}
	if req.Auth.Enabled && strings.TrimSpace(req.Auth.ClientSecret) == "" {
		return nil, "auth_client_secret is required when auth is enabled for schedules", http.StatusBadRequest
	}

	scheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		return nil, "scheduled_at must be RFC3339 format (e.g. 2026-03-15T02:00:00Z)", http.StatusBadRequest
	}
	if scheduledAt.Before(time.Now().Add(-1 * time.Minute)) {
		return nil, "scheduled_at must be in the future", http.StatusBadRequest
	}

	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, "invalid timezone: " + tz, http.StatusBadRequest
	}

	recType := req.RecurrenceType
	if recType == "" {
		recType = "once"
	}
	validRecurrences := map[string]bool{"once": true, "hourly": true, "daily": true, "weekly": true, "monthly": true}
	if !validRecurrences[recType] {
		return nil, "recurrence_type must be one of: once, hourly, daily, weekly, monthly", http.StatusBadRequest
	}

	durationS, err := scheduler.EstimateDurationSeconds(req)
	if err != nil {
		return nil, err.Error(), http.StatusBadRequest
	}

	recEnd, err := parseRecurrenceEnd(req.RecurrenceEnd)
	if err != nil {
		return nil, "recurrence_end must be RFC3339 format", http.StatusBadRequest
	}

	return &validatedScheduleCreate{
		scheduledAt: scheduledAt,
		timezone:    tz,
		recType:     recType,
		recEnd:      recEnd,
		durationS:   durationS,
	}, "", 0
}

func parseRecurrenceEnd(value *string) (*time.Time, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func normalizeOccurrence(t time.Time) time.Time {
	return t.UTC().Truncate(time.Second)
}

func sameOccurrence(a, b time.Time) bool {
	return normalizeOccurrence(a).Equal(normalizeOccurrence(b))
}

func hasPastOccurrence(candidate time.Time) bool {
	return normalizeOccurrence(candidate).Before(normalizeOccurrence(time.Now().UTC().Add(-1 * time.Minute)))
}

func skippedOccurrenceExists(values []time.Time, candidate time.Time) bool {
	for _, value := range values {
		if sameOccurrence(value, candidate) {
			return true
		}
	}
	return false
}

func appendSkippedOccurrence(values []time.Time, candidate time.Time) []time.Time {
	if skippedOccurrenceExists(values, candidate) {
		return values
	}
	return append(values, normalizeOccurrence(candidate))
}

func filterSkippedOccurrencesBefore(values []time.Time, cutoff time.Time) []time.Time {
	if len(values) == 0 {
		return nil
	}
	var filtered []time.Time
	for _, value := range values {
		if normalizeOccurrence(value).Before(normalizeOccurrence(cutoff)) {
			filtered = append(filtered, normalizeOccurrence(value))
		}
	}
	return filtered
}

func parseDeleteScope(r *http.Request, recurring bool) (scheduleDeleteScope, bool) {
	switch r.URL.Query().Get("scope") {
	case "single":
		return deleteScopeSingle, true
	case "future":
		return deleteScopeFuture, true
	case "":
		if recurring {
			return deleteScopeFuture, false
		}
		return deleteScopeSingle, false
	default:
		return "", false
	}
}

func parseDeleteOccurrence(r *http.Request, fallback time.Time) (time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("occurrence"))
	if raw == "" {
		return normalizeOccurrence(fallback), nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return normalizeOccurrence(parsed), nil
}

func (h *ScheduleHandler) writeScheduleConflict(w http.ResponseWriter, conflict any) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":    "schedule conflicts with existing test",
		"conflict": conflict,
	})
}

func cloneScheduledTest(existing *model.ScheduledTest) *model.ScheduledTest {
	clone := *existing
	if existing.Stages != nil {
		clone.Stages = append([]model.Stage(nil), existing.Stages...)
	}
	if existing.SkippedOccurrences != nil {
		clone.SkippedOccurrences = append([]time.Time(nil), existing.SkippedOccurrences...)
	}
	return &clone
}

func (h *ScheduleHandler) checkScheduleOverlap(ctx context.Context, scheduledAt time.Time, durationS int, excludeID string) (*model.ScheduleConflict, error) {
	checker := h.overlapChecker
	if checker == nil {
		checker = scheduler.CheckOverlap
	}
	proposed := scheduler.TimeSlot{
		Start: scheduledAt,
		End:   scheduledAt.Add(time.Duration(durationS) * time.Second),
	}
	return checker(ctx, h.store, proposed, excludeID, h.activeRunningTest())
}

func (h *ScheduleHandler) prepareUpdatedSchedule(ctx context.Context, existing *model.ScheduledTest, req *model.CreateScheduleRequest) (*model.ScheduledTest, *model.ScheduleConflict, error) {
	candidate := cloneScheduledTest(existing)
	if err := applyScheduleUpdates(candidate, req); err != nil {
		return nil, nil, httpStatusError{Message: "invalid scheduled_at", Code: http.StatusBadRequest}
	}
	authCfg, err := buildStoredAuthConfig(req.Auth, &existing.AuthConfig, h.secretSvc, req.Auth.Enabled)
	if err != nil {
		return nil, nil, httpStatusError{Message: err.Error(), Code: http.StatusBadRequest}
	}
	candidate.AuthConfig = authCfg

	conflict, err := h.checkScheduleOverlap(ctx, candidate.ScheduledAt, candidate.EstimatedDurationS, candidate.ID)
	if err != nil {
		return nil, nil, httpStatusError{Message: "internal error during conflict check", Code: http.StatusInternalServerError}
	}
	return candidate, conflict, nil
}

func applyScheduleUpdates(existing *model.ScheduledTest, req *model.CreateScheduleRequest) error {
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.ProjectName != "" {
		existing.ProjectName = req.ProjectName
	}
	if req.URL != "" {
		existing.URL = req.URL
	}
	if req.Mode != "" {
		existing.Mode = req.Mode
	}
	if req.Executor != "" {
		existing.Executor = req.Executor
	}
	if req.Stages != nil {
		existing.Stages = req.Stages
	}
	if req.ScriptContent != "" {
		existing.ScriptContent = req.ScriptContent
	}
	if req.ConfigContent != "" {
		existing.ConfigContent = req.ConfigContent
	}
	if req.HTTPMethod != "" {
		existing.HTTPMethod = req.HTTPMethod
	}
	if req.ContentType != "" {
		existing.ContentType = req.ContentType
	}
	if req.PayloadJSON != "" {
		existing.PayloadJSON = req.PayloadJSON
	}
	if req.PayloadTargetKiB > 0 {
		existing.PayloadTargetKiB = req.PayloadTargetKiB
	}
	if req.ScheduledAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduledAt)
		if err != nil {
			return err
		}
		existing.ScheduledAt = t
	}
	if req.RecurrenceType != "" {
		existing.RecurrenceType = req.RecurrenceType
	}
	if req.Timezone != "" {
		existing.Timezone = req.Timezone
	}

	durationS, err := scheduler.EstimateDurationSeconds(req)
	if err == nil && durationS > 0 {
		existing.EstimatedDurationS = durationS
	}

	existing.Status = "scheduled"
	return nil
}

// Create creates a new scheduled test.
func (h *ScheduleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	validated, errMsg, statusCode := h.validateCreateRequest(&req)
	if errMsg != "" {
		httpError(w, errMsg, statusCode)
		return
	}

	// Check for overlap
	conflict, err := h.checkScheduleOverlap(r.Context(), validated.scheduledAt, validated.durationS, "")
	if err != nil {
		h.logger.Error("overlap check failed", "error", err)
		httpError(w, "internal error during conflict check", http.StatusInternalServerError)
		return
	}
	if conflict != nil {
		h.writeScheduleConflict(w, conflict)
		return
	}

	userID := middleware.GetUserID(r.Context())
	username := middleware.GetUsername(r.Context())

	st := &model.ScheduledTest{
		ID:                 uuid.New().String(),
		Name:               req.Name,
		ProjectName:        req.ProjectName,
		URL:                req.URL,
		Mode:               req.Mode,
		Executor:           req.Executor,
		Stages:             req.Stages,
		VUs:                req.VUs,
		Duration:           req.Duration,
		Rate:               req.Rate,
		TimeUnit:           req.TimeUnit,
		PreAllocatedVUs:    req.PreAllocatedVUs,
		MaxVUs:             req.MaxVUs,
		SleepSeconds:       req.SleepSeconds,
		ScriptContent:      req.ScriptContent,
		ConfigContent:      req.ConfigContent,
		HTTPMethod:         req.HTTPMethod,
		ContentType:        req.ContentType,
		PayloadJSON:        req.PayloadJSON,
		PayloadTargetKiB:   req.PayloadTargetKiB,
		ScheduledAt:        validated.scheduledAt,
		EstimatedDurationS: validated.durationS,
		Timezone:           validated.timezone,
		RecurrenceType:     validated.recType,
		RecurrenceRule:     req.RecurrenceRule,
		RecurrenceEnd:      validated.recEnd,
		Status:             "scheduled",
		Paused:             false,
		UserID:             userID,
		Username:           username,
	}
	authCfg, err := buildStoredAuthConfig(req.Auth, nil, h.secretSvc, req.Auth.Enabled)
	if err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}
	st.AuthConfig = authCfg

	if err := h.store.CreateSchedule(r.Context(), st); err != nil {
		h.logger.Error("failed to create schedule", "error", err)
		httpError(w, "failed to create schedule", http.StatusInternalServerError)
		return
	}

	h.logger.Info("schedule created",
		"id", st.ID, "name", st.Name, "scheduled_at", st.ScheduledAt, "recurrence", validated.recType)
	writeJSON(w, http.StatusCreated, st)
}

// List returns all scheduled tests (global visibility).
func (h *ScheduleHandler) List(w http.ResponseWriter, r *http.Request) {
	schedules, err := h.store.ListSchedules(r.Context())
	if err != nil {
		h.logger.Error("failed to list schedules", "error", err)
		httpError(w, "failed to list schedules", http.StatusInternalServerError)
		return
	}
	if schedules == nil {
		schedules = []model.ScheduledTest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": schedules})
}

// Get returns a single scheduled test.
func (h *ScheduleHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	st, err := h.loadSchedule(r, id)
	if err != nil {
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if st == nil {
		httpError(w, "schedule not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// Update modifies a scheduled test. Owner or admin only.
func (h *ScheduleHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, ok := h.managedSchedule(w, r, id)
	if !ok {
		return
	}

	if existing.Status == "running" {
		httpError(w, "cannot modify a running schedule", http.StatusConflict)
		return
	}

	var req model.CreateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := normalizeScheduleRequestPayload(&req); err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Auth.Enabled && strings.TrimSpace(req.Auth.ClientSecret) == "" && !existing.AuthConfig.SecretConfigured {
		httpError(w, "auth_client_secret is required when auth is enabled for schedules", http.StatusBadRequest)
		return
	}

	candidate, conflict, err := h.prepareUpdatedSchedule(r.Context(), existing, &req)
	if err != nil {
		if statusErr, ok := err.(httpStatusError); ok {
			httpError(w, statusErr.Message, statusErr.Code)
			return
		}
		httpError(w, "failed to update schedule", http.StatusInternalServerError)
		return
	}
	if conflict != nil {
		h.writeScheduleConflict(w, conflict)
		return
	}

	if err := h.store.UpdateSchedule(r.Context(), candidate); err != nil {
		httpError(w, "failed to update schedule", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, candidate)
}

// Delete removes a scheduled test. Owner or admin only.
func (h *ScheduleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, ok := h.managedSchedule(w, r, id)
	if !ok {
		return
	}

	recurring := existing.RecurrenceType != "once"
	scope, validScope := parseDeleteScope(r, recurring)
	if scope == "" {
		httpError(w, "invalid delete scope", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("scope") != "" && !validScope {
		httpError(w, "invalid delete scope", http.StatusBadRequest)
		return
	}

	occurrence, err := parseDeleteOccurrence(r, existing.ScheduledAt)
	if err != nil {
		httpError(w, "occurrence must be RFC3339 format", http.StatusBadRequest)
		return
	}
	if hasPastOccurrence(occurrence) {
		httpError(w, "past schedule occurrences are preserved and cannot be deleted", http.StatusConflict)
		return
	}

	if existing.Status == "running" {
		httpError(w, "cannot delete a running schedule", http.StatusConflict)
		return
	}

	execCount, err := h.store.CountScheduleExecutions(r.Context(), id)
	if err != nil {
		httpError(w, "failed to inspect schedule history", http.StatusInternalServerError)
		return
	}

	if !recurring {
		if err := h.store.DeleteSchedule(r.Context(), id); err != nil {
			httpError(w, "failed to delete schedule", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "deleted",
			"scope":  string(deleteScopeSingle),
		})
		return
	}

	switch scope {
	case deleteScopeSingle:
		existing.SkippedOccurrences = appendSkippedOccurrence(existing.SkippedOccurrences, occurrence)

		if sameOccurrence(existing.ScheduledAt, occurrence) {
			nextAt, nextErr := scheduler.NextIncludedOccurrence(existing.ScheduledAt, existing.RecurrenceType, existing.Timezone, existing.RecurrenceEnd, existing.SkippedOccurrences)
			if nextErr != nil {
				httpError(w, "failed to advance recurring schedule", http.StatusInternalServerError)
				return
			}
			if nextAt.IsZero() {
				if execCount == 0 {
					if err := h.store.DeleteSchedule(r.Context(), id); err != nil {
						httpError(w, "failed to delete schedule", http.StatusInternalServerError)
						return
					}
					writeJSON(w, http.StatusOK, map[string]string{
						"status": "deleted",
						"scope":  string(deleteScopeSingle),
					})
					return
				}
				cutoff := occurrence.Add(-1 * time.Second)
				existing.RecurrenceEnd = &cutoff
				existing.Status = "completed"
			} else {
				existing.ScheduledAt = nextAt
				existing.Status = "scheduled"
			}
		}

		if err := h.store.UpdateSchedule(r.Context(), existing); err != nil {
			httpError(w, "failed to update schedule", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "updated",
			"scope":  string(deleteScopeSingle),
		})
		return
	case deleteScopeFuture:
		cutoff := occurrence.Add(-1 * time.Second)
		existing.RecurrenceEnd = &cutoff
		existing.SkippedOccurrences = filterSkippedOccurrencesBefore(existing.SkippedOccurrences, occurrence)

		if !existing.ScheduledAt.Before(occurrence) {
			if execCount == 0 {
				if err := h.store.DeleteSchedule(r.Context(), id); err != nil {
					httpError(w, "failed to delete schedule", http.StatusInternalServerError)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{
					"status": "deleted",
					"scope":  string(deleteScopeFuture),
				})
				return
			}
			existing.Status = "completed"
		}

		if err := h.store.UpdateSchedule(r.Context(), existing); err != nil {
			httpError(w, "failed to update schedule", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "updated",
			"scope":  string(deleteScopeFuture),
		})
		return
	default:
		httpError(w, "invalid delete scope", http.StatusBadRequest)
		return
	}
}

// Pause pauses a recurring schedule. Owner or admin only.
func (h *ScheduleHandler) Pause(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.managedSchedule(w, r, id); !ok {
		return
	}

	if err := h.store.PauseSchedule(r.Context(), id, true); err != nil {
		httpError(w, "failed to pause schedule", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

// Resume resumes a paused schedule. Owner or admin only.
func (h *ScheduleHandler) Resume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.managedSchedule(w, r, id); !ok {
		return
	}

	if err := h.store.PauseSchedule(r.Context(), id, false); err != nil {
		httpError(w, "failed to resume schedule", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

// RunNow triggers immediate execution of a scheduled test. Owner or admin only.
func (h *ScheduleHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, ok := h.managedSchedule(w, r, id)
	if !ok {
		return
	}

	if activeID := h.active.GetActiveTestID(); activeID != "" {
		httpError(w, "a test is already running: "+activeID, http.StatusConflict)
		return
	}

	// Trigger the scheduler to execute this test now
	existing.ScheduledAt = time.Now().Add(-1 * time.Second) // make it "due"
	existing.Status = "scheduled"
	existing.Paused = false
	if err := h.store.UpdateSchedule(r.Context(), existing); err != nil {
		httpError(w, "failed to trigger schedule", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "triggered", "message": "Schedule will execute within the next 10 seconds"})
}

// Calendar returns scheduled test events expanded within a time range.
func (h *ScheduleHandler) Calendar(w http.ResponseWriter, r *http.Request) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		from = time.Now().Truncate(24 * time.Hour)
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		to = from.Add(7 * 24 * time.Hour)
	}

	schedules, err := h.store.ListSchedules(r.Context())
	if err != nil {
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	var events []model.CalendarEvent
	for _, s := range schedules {
		if s.Status == "cancelled" {
			continue
		}

		slots := scheduler.ExpandOccurrences(
			s.ScheduledAt, s.EstimatedDurationS,
			s.RecurrenceType, s.Timezone, s.RecurrenceEnd, s.SkippedOccurrences,
			from, to,
		)
		for _, slot := range slots {
			events = append(events, model.CalendarEvent{
				ID:              s.ID,
				Name:            s.Name,
				ProjectName:     s.ProjectName,
				Start:           slot.Start.Format(time.RFC3339),
				End:             slot.End.Format(time.RFC3339),
				OccurrenceStart: slot.Start.Format(time.RFC3339),
				Status:          s.Status,
				RecurrenceType:  s.RecurrenceType,
				Username:        s.Username,
				UserID:          s.UserID,
			})
		}
	}

	if events == nil {
		events = []model.CalendarEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// CheckConflict validates if a proposed time slot has conflicts.
func (h *ScheduleHandler) CheckConflict(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Start     string `json:"start"`
		DurationS int    `json:"duration_s"`
		ExcludeID string `json:"exclude_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request", http.StatusBadRequest)
		return
	}

	start, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		httpError(w, "start must be RFC3339", http.StatusBadRequest)
		return
	}

	proposed := scheduler.TimeSlot{
		Start: start,
		End:   start.Add(time.Duration(req.DurationS) * time.Second),
	}

	conflict, err := scheduler.CheckOverlap(r.Context(), h.store, proposed, req.ExcludeID, h.activeRunningTest())
	if err != nil {
		httpError(w, "conflict check failed", http.StatusInternalServerError)
		return
	}

	resp := map[string]any{"conflict": conflict != nil}
	if conflict != nil {
		resp["conflicting_schedule"] = conflict
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListExecutions returns the execution history for a schedule.
func (h *ScheduleHandler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	execs, err := h.store.ListExecutions(r.Context(), id)
	if err != nil {
		httpError(w, "failed to list executions", http.StatusInternalServerError)
		return
	}
	if execs == nil {
		execs = []model.ScheduleExecution{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": execs})
}

// canManage checks if the current user can manage (edit/delete/pause) the schedule.
func (h *ScheduleHandler) canManage(r *http.Request, s *model.ScheduledTest) bool {
	role := middleware.GetRole(r.Context())
	userID := middleware.GetUserID(r.Context())
	return role == "admin" || s.UserID == userID
}
