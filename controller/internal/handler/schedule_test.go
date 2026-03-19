package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scheduler"
	"github.com/shiva-load-testing/controller/internal/store"
)

func TestPrepareUpdatedScheduleRejectsConflictingUpdate(t *testing.T) {
	originalStart := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	updatedStart := time.Date(2026, 3, 20, 11, 0, 0, 0, time.UTC)

	existing := &model.ScheduledTest{
		ID:                 "sched-1",
		Name:               "Original",
		ProjectName:        "checkout",
		URL:                "https://api.example.com/orders",
		Mode:               "builder",
		Executor:           "constant-vus",
		Duration:           "1m",
		ScheduledAt:        originalStart,
		EstimatedDurationS: 90,
		Timezone:           "UTC",
		RecurrenceType:     "once",
		Status:             "scheduled",
	}

	var gotExcludeID string
	h := &ScheduleHandler{
		overlapChecker: func(_ context.Context, _ *store.Store, proposed scheduler.TimeSlot, excludeID string, running *scheduler.RunningTestInfo) (*model.ScheduleConflict, error) {
			gotExcludeID = excludeID
			if running != nil {
				t.Fatalf("expected update conflict check to omit running test when none is active")
			}
			if !proposed.Start.Equal(updatedStart) {
				t.Fatalf("expected updated schedule start %s, got %s", updatedStart, proposed.Start)
			}
			if got := proposed.End.Sub(proposed.Start); got != 150*time.Second {
				t.Fatalf("expected updated schedule duration 150s including buffer, got %s", got)
			}
			return &model.ScheduleConflict{
				ScheduleID:   "sched-2",
				ScheduleName: "Existing overlap",
				Start:        updatedStart.Format(time.RFC3339),
				End:          updatedStart.Add(5 * time.Minute).Format(time.RFC3339),
				Type:         "scheduled",
			}, nil
		},
	}

	req := &model.CreateScheduleRequest{
		ScheduledAt:        updatedStart.Format(time.RFC3339),
		EstimatedDurationS: 120,
	}

	candidate, conflict, err := h.prepareUpdatedSchedule(context.Background(), existing, req)
	if err != nil {
		t.Fatalf("expected conflict result without error, got %v", err)
	}
	if conflict == nil {
		t.Fatalf("expected conflicting update to return a schedule conflict")
	}
	if gotExcludeID != existing.ID {
		t.Fatalf("expected update overlap check to exclude %q, got %q", existing.ID, gotExcludeID)
	}
	if existing.ScheduledAt != originalStart {
		t.Fatalf("expected original schedule start to remain unchanged")
	}
	if candidate == nil {
		t.Fatalf("expected updated candidate even when conflict is returned")
	}
	if !candidate.ScheduledAt.Equal(updatedStart) {
		t.Fatalf("expected candidate to carry updated start time")
	}
	if candidate.EstimatedDurationS != 150 {
		t.Fatalf("expected candidate duration to include scheduling buffer, got %d", candidate.EstimatedDurationS)
	}
}

func TestPrepareUpdatedScheduleReturnsBadRequestForInvalidScheduledAt(t *testing.T) {
	h := &ScheduleHandler{}
	existing := &model.ScheduledTest{
		ID:                 "sched-1",
		Name:               "Original",
		ProjectName:        "checkout",
		URL:                "https://api.example.com/orders",
		Mode:               "builder",
		ScheduledAt:        time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC),
		EstimatedDurationS: 90,
		Timezone:           "UTC",
		RecurrenceType:     "once",
		Status:             "scheduled",
	}

	_, _, err := h.prepareUpdatedSchedule(context.Background(), existing, &model.CreateScheduleRequest{
		ScheduledAt: "not-a-timestamp",
	})
	if err == nil {
		t.Fatalf("expected invalid scheduled_at to fail")
	}

	statusErr, ok := err.(httpStatusError)
	if !ok {
		t.Fatalf("expected httpStatusError, got %T", err)
	}
	if statusErr.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request status, got %d", statusErr.Code)
	}
	if statusErr.Message != "invalid scheduled_at" {
		t.Fatalf("expected invalid scheduled_at message, got %q", statusErr.Message)
	}
}
