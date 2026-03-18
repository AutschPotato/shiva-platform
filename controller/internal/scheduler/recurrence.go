package scheduler

import (
	"fmt"
	"time"
)

// NextOccurrence calculates the next execution time for a recurring schedule.
// Returns zero time if the schedule is one-time or past its end date.
// All calculations are timezone-aware to handle DST transitions correctly.
func NextOccurrence(current time.Time, recurrenceType, timezone string, recurrenceEnd *time.Time) (time.Time, error) {
	if recurrenceType == "once" {
		return time.Time{}, nil
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}

	// Work in the user's timezone for correct DST handling
	localCurrent := current.In(loc)

	var next time.Time
	switch recurrenceType {
	case "hourly":
		next = localCurrent.Add(1 * time.Hour)
	case "daily":
		next = localCurrent.AddDate(0, 0, 1)
	case "weekly":
		next = localCurrent.AddDate(0, 0, 7)
	case "monthly":
		next = localCurrent.AddDate(0, 1, 0)
	default:
		return time.Time{}, fmt.Errorf("unknown recurrence type: %s", recurrenceType)
	}

	// Convert back to UTC for storage
	nextUTC := next.UTC()

	// Check if past end date
	if recurrenceEnd != nil && nextUTC.After(*recurrenceEnd) {
		return time.Time{}, nil
	}

	return nextUTC, nil
}

// ExpandOccurrences generates all occurrences of a recurring schedule within a time range.
// Used for calendar view and overlap detection.
func ExpandOccurrences(baseTime time.Time, durationS int, recurrenceType, timezone string, recurrenceEnd *time.Time, rangeStart, rangeEnd time.Time) []TimeSlot {
	if recurrenceType == "once" {
		end := baseTime.Add(time.Duration(durationS) * time.Second)
		if baseTime.Before(rangeEnd) && end.After(rangeStart) {
			return []TimeSlot{{Start: baseTime, End: end}}
		}
		return nil
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}

	var slots []TimeSlot
	current := baseTime

	// Generate occurrences up to rangeEnd, max 100 to prevent infinite loops
	for i := 0; i < 100; i++ {
		if current.After(rangeEnd) {
			break
		}
		if recurrenceEnd != nil && current.After(*recurrenceEnd) {
			break
		}

		end := current.Add(time.Duration(durationS) * time.Second)
		if end.After(rangeStart) && current.Before(rangeEnd) {
			slots = append(slots, TimeSlot{Start: current, End: end})
		}

		// Advance to next occurrence
		localCurrent := current.In(loc)
		switch recurrenceType {
		case "hourly":
			current = localCurrent.Add(1 * time.Hour).UTC()
		case "daily":
			current = localCurrent.AddDate(0, 0, 1).UTC()
		case "weekly":
			current = localCurrent.AddDate(0, 0, 7).UTC()
		case "monthly":
			current = localCurrent.AddDate(0, 1, 0).UTC()
		default:
			break
		}
	}

	return slots
}
