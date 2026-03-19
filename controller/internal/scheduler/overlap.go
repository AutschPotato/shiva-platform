package scheduler

import (
	"context"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/store"
)

// TimeSlot represents a time window for overlap detection.
type TimeSlot struct {
	Start time.Time
	End   time.Time
}

// RunningTestInfo provides information about the currently running test.
type RunningTestInfo struct {
	TestID             string
	StartTime          time.Time
	EstimatedDurationS int // 0 = unknown; expected to already include any scheduling buffer
}

// CheckOverlap checks if a proposed time slot conflicts with existing schedules or a running test.
// Returns the first conflict found, or nil if no conflicts.
func CheckOverlap(ctx context.Context, st *store.Store, proposed TimeSlot, excludeID string, running *RunningTestInfo) (*model.ScheduleConflict, error) {
	// 1. Check against currently running test
	if conflict := runningTestConflict(proposed, running); conflict != nil {
		return conflict, nil
	}

	// 2. Check against one-time and non-recurring scheduled tests in DB
	overlapping, err := st.GetOverlappingSchedules(ctx, proposed.Start, proposed.End, excludeID)
	if err != nil {
		return nil, err
	}
	if len(overlapping) > 0 {
		return scheduledConflict(overlapping[0]), nil
	}

	// 3. Check against expanded recurring schedules
	recurring, err := st.GetRecurringSchedules(ctx)
	if err != nil {
		return nil, err
	}
	return recurringScheduleConflict(proposed, recurring, excludeID), nil
}

func runningTestConflict(proposed TimeSlot, running *RunningTestInfo) *model.ScheduleConflict {
	if running == nil || running.TestID == "" {
		return nil
	}
	slot := runningTimeSlot(running)
	if !timeSlotsOverlap(proposed, slot) {
		return nil
	}
	return &model.ScheduleConflict{
		Type:  "running",
		Start: slot.Start.Format(time.RFC3339),
		End:   slot.End.Format(time.RFC3339),
	}
}

func recurringScheduleConflict(proposed TimeSlot, recurring []model.ScheduledTest, excludeID string) *model.ScheduleConflict {
	for _, rec := range recurring {
		if rec.ID == excludeID {
			continue
		}
		slots := ExpandOccurrences(
			rec.ScheduledAt,
			rec.EstimatedDurationS,
			rec.RecurrenceType,
			rec.Timezone,
			rec.RecurrenceEnd,
			rec.SkippedOccurrences,
			proposed.Start.Add(-24*time.Hour),
			proposed.End.Add(24*time.Hour),
		)
		for _, slot := range slots {
			if timeSlotsOverlap(proposed, slot) {
				return &model.ScheduleConflict{
					ScheduleID:   rec.ID,
					ScheduleName: rec.Name,
					Start:        slot.Start.Format(time.RFC3339),
					End:          slot.End.Format(time.RFC3339),
					Type:         "scheduled",
				}
			}
		}
	}
	return nil
}

func runningTimeSlot(running *RunningTestInfo) TimeSlot {
	end := running.StartTime.Add(2 * time.Hour)
	if running.EstimatedDurationS > 0 {
		end = running.StartTime.Add(time.Duration(running.EstimatedDurationS) * time.Second)
	}
	return TimeSlot{
		Start: running.StartTime,
		End:   end,
	}
}

func timeSlotsOverlap(a, b TimeSlot) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}

func scheduledConflict(s model.ScheduledTest) *model.ScheduleConflict {
	end := s.ScheduledAt.Add(time.Duration(s.EstimatedDurationS) * time.Second)
	return &model.ScheduleConflict{
		ScheduleID:   s.ID,
		ScheduleName: s.Name,
		Start:        s.ScheduledAt.Format(time.RFC3339),
		End:          end.Format(time.RFC3339),
		Type:         "scheduled",
	}
}
