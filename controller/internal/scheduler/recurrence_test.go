package scheduler

import (
	"testing"
	"time"
)

func TestNextIncludedOccurrenceSkipsExcludedFutureOccurrence(t *testing.T) {
	base := time.Date(2026, 3, 20, 8, 0, 0, 0, time.UTC)
	skipped := []time.Time{
		time.Date(2026, 3, 21, 8, 0, 0, 0, time.UTC),
	}

	next, err := NextIncludedOccurrence(base, "daily", "UTC", nil, skipped)
	if err != nil {
		t.Fatalf("NextIncludedOccurrence returned error: %v", err)
	}

	want := time.Date(2026, 3, 22, 8, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("NextIncludedOccurrence = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestExpandOccurrencesExcludesSkippedOccurrences(t *testing.T) {
	base := time.Date(2026, 3, 20, 8, 0, 0, 0, time.UTC)
	skipped := []time.Time{
		time.Date(2026, 3, 21, 8, 0, 0, 0, time.UTC),
	}

	slots := ExpandOccurrences(
		base,
		1800,
		"daily",
		"UTC",
		nil,
		skipped,
		time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC),
	)

	if len(slots) != 2 {
		t.Fatalf("ExpandOccurrences returned %d slots, want 2", len(slots))
	}

	if slots[0].Start.Format(time.RFC3339) != "2026-03-20T08:00:00Z" {
		t.Fatalf("first slot starts at %s", slots[0].Start.Format(time.RFC3339))
	}
	if slots[1].Start.Format(time.RFC3339) != "2026-03-22T08:00:00Z" {
		t.Fatalf("second slot starts at %s", slots[1].Start.Format(time.RFC3339))
	}
}
