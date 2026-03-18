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
	EstimatedDurationS int // 0 = unknown
}

// CheckOverlap checks if a proposed time slot conflicts with existing schedules or a running test.
// Returns the first conflict found, or nil if no conflicts.
func CheckOverlap(ctx context.Context, st *store.Store, proposed TimeSlot, excludeID string, running *RunningTestInfo) (*model.ScheduleConflict, error) {
	// 1. Check against currently running test
	if running != nil && running.TestID != "" {
		var runEnd time.Time
		if running.EstimatedDurationS > 0 {
			runEnd = running.StartTime.Add(time.Duration(running.EstimatedDurationS+testBufferSeconds) * time.Second)
		} else {
			// Unknown duration — assume worst case (2h)
			runEnd = running.StartTime.Add(2 * time.Hour)
		}
		if proposed.Start.Before(runEnd) {
			return &model.ScheduleConflict{
				Type:  "running",
				Start: running.StartTime.Format(time.RFC3339),
				End:   runEnd.Format(time.RFC3339),
			}, nil
		}
	}

	// 2. Check against one-time and non-recurring scheduled tests in DB
	overlapping, err := st.GetOverlappingSchedules(ctx, proposed.Start, proposed.End, excludeID)
	if err != nil {
		return nil, err
	}
	if len(overlapping) > 0 {
		s := overlapping[0]
		end := s.ScheduledAt.Add(time.Duration(s.EstimatedDurationS+testBufferSeconds) * time.Second)
		return &model.ScheduleConflict{
			ScheduleID:   s.ID,
			ScheduleName: s.Name,
			Start:        s.ScheduledAt.Format(time.RFC3339),
			End:          end.Format(time.RFC3339),
			Type:         "scheduled",
		}, nil
	}

	// 3. Check against expanded recurring schedules
	recurring, err := st.GetRecurringSchedules(ctx)
	if err != nil {
		return nil, err
	}
	for _, rec := range recurring {
		if rec.ID == excludeID {
			continue
		}
		slots := ExpandOccurrences(rec.ScheduledAt, rec.EstimatedDurationS+testBufferSeconds,
			rec.RecurrenceType, rec.Timezone, rec.RecurrenceEnd, proposed.Start.Add(-24*time.Hour), proposed.End.Add(24*time.Hour))
		for _, slot := range slots {
			if proposed.Start.Before(slot.End) && slot.Start.Before(proposed.End) {
				return &model.ScheduleConflict{
					ScheduleID:   rec.ID,
					ScheduleName: rec.Name,
					Start:        slot.Start.Format(time.RFC3339),
					End:          slot.End.Format(time.RFC3339),
					Type:         "scheduled",
				}, nil
			}
		}
	}

	return nil, nil
}
