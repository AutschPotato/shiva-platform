package scheduler

import (
	"testing"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
)

func TestTimeSlotsOverlapAllowsTouchingBoundaries(t *testing.T) {
	start := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	first := TimeSlot{Start: start, End: start.Add(150 * time.Second)}
	second := TimeSlot{Start: first.End, End: first.End.Add(2 * time.Minute)}

	if timeSlotsOverlap(first, second) {
		t.Fatalf("expected touching time slots to remain non-overlapping")
	}

	overlapping := TimeSlot{Start: first.End.Add(-1 * time.Second), End: first.End.Add(30 * time.Second)}
	if !timeSlotsOverlap(first, overlapping) {
		t.Fatalf("expected one-second overlap to be detected")
	}
}

func TestRecurringScheduleConflictDetectsExpandedOccurrence(t *testing.T) {
	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	recEnd := base.Add(3 * time.Hour)

	conflict := recurringScheduleConflict(
		TimeSlot{
			Start: base.Add(1*time.Hour + 60*time.Second),
			End:   base.Add(1*time.Hour + 180*time.Second),
		},
		[]model.ScheduledTest{
			{
				ID:                 "series-1",
				Name:               "Hourly schedule",
				ScheduledAt:        base,
				EstimatedDurationS: 150,
				Timezone:           "UTC",
				RecurrenceType:     "hourly",
				RecurrenceEnd:      &recEnd,
			},
		},
		"",
	)

	if conflict == nil {
		t.Fatalf("expected recurring occurrence conflict to be detected")
	}
	if conflict.ScheduleID != "series-1" {
		t.Fatalf("expected recurring conflict to reference series-1, got %q", conflict.ScheduleID)
	}
	if conflict.Start != base.Add(1*time.Hour).Format(time.RFC3339) {
		t.Fatalf("expected recurring conflict to point at the expanded occurrence start")
	}
	if conflict.End != base.Add(1*time.Hour+150*time.Second).Format(time.RFC3339) {
		t.Fatalf("expected recurring conflict to use stored buffered duration")
	}
}

func TestRecurringScheduleConflictHonorsExcludedScheduleID(t *testing.T) {
	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	recEnd := base.Add(2 * time.Hour)

	conflict := recurringScheduleConflict(
		TimeSlot{
			Start: base.Add(1 * time.Hour),
			End:   base.Add(1*time.Hour + 60*time.Second),
		},
		[]model.ScheduledTest{
			{
				ID:                 "series-1",
				Name:               "Hourly schedule",
				ScheduledAt:        base,
				EstimatedDurationS: 150,
				Timezone:           "UTC",
				RecurrenceType:     "hourly",
				RecurrenceEnd:      &recEnd,
			},
		},
		"series-1",
	)

	if conflict != nil {
		t.Fatalf("expected excluded schedule ID to skip its own recurring overlap")
	}
}

func TestRunningTestConflictUsesBufferedDurationAsStored(t *testing.T) {
	start := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	running := &RunningTestInfo{
		TestID:             "run-1",
		StartTime:          start,
		EstimatedDurationS: 150,
	}

	touching := TimeSlot{
		Start: start.Add(150 * time.Second),
		End:   start.Add(5 * time.Minute),
	}
	if conflict := runningTestConflict(touching, running); conflict != nil {
		t.Fatalf("expected proposal starting exactly at running end to be allowed")
	}

	overlapping := TimeSlot{
		Start: start.Add(149 * time.Second),
		End:   start.Add(5 * time.Minute),
	}
	if conflict := runningTestConflict(overlapping, running); conflict == nil {
		t.Fatalf("expected overlap with running test to be detected")
	}
}
