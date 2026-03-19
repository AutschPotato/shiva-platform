package scheduler

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
)

const testBufferSeconds = 30

// EstimateDurationSeconds calculates the expected test duration from a schedule request.
// Returns the duration in seconds including a 30s buffer for worker restart.
// Returns an error if duration cannot be determined and estimated_duration_s is not provided.
func EstimateDurationSeconds(req *model.CreateScheduleRequest) (int, error) {
	// User provided an explicit estimate — use it directly
	if req.EstimatedDurationS > 0 {
		return req.EstimatedDurationS + testBufferSeconds, nil
	}

	var durationS int

	if req.Mode == "builder" || (req.Mode == "" && req.URL != "") {
		durationS = estimateFromBuilder(req)
	}

	if durationS == 0 && req.ConfigContent != "" {
		durationS = estimateFromConfig(req.ConfigContent)
	}

	if durationS == 0 && req.ScriptContent != "" {
		durationS = estimateFromScript(req.ScriptContent)
	}

	if durationS <= 0 {
		return 0, fmt.Errorf("cannot determine test duration: provide estimated_duration_s or ensure config/script has a duration")
	}

	return durationS + testBufferSeconds, nil
}

// estimateFromBuilder extracts duration from builder-mode parameters.
func estimateFromBuilder(req *model.CreateScheduleRequest) int {
	executor := req.Executor
	if executor == "" {
		executor = "ramping-vus"
	}

	switch executor {
	case "ramping-vus", "ramping-arrival-rate":
		total := 0
		for _, s := range req.Stages {
			total += scriptgen.ParseK6Duration(s.Duration)
		}
		return total

	case "constant-vus", "constant-arrival-rate":
		if req.Duration != "" {
			return scriptgen.ParseK6Duration(req.Duration)
		}
	}

	return 0
}

// estimateFromConfig extracts the maximum scenario duration from a config JSON.
func estimateFromConfig(configContent string) int {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(configContent), &parsed); err != nil {
		return 0
	}

	scenarios, ok := parsed["scenarios"]
	if !ok {
		// Try top-level duration
		if dur, ok := parsed["duration"].(string); ok {
			return scriptgen.ParseK6Duration(dur)
		}
		return 0
	}

	scenarioMap, ok := scenarios.(map[string]any)
	if !ok {
		return 0
	}

	maxDuration := 0
	for _, sc := range scenarioMap {
		scMap, ok := sc.(map[string]any)
		if !ok {
			continue
		}

		d := 0
		// Check direct duration field
		if dur, ok := scMap["duration"].(string); ok {
			d = scriptgen.ParseK6Duration(dur)
		}

		// Check stages (sum of stage durations)
		if stages, ok := scMap["stages"].([]any); ok {
			stageTotal := 0
			for _, stageRaw := range stages {
				stageMap, ok := stageRaw.(map[string]any)
				if !ok {
					continue
				}
				if dur, ok := stageMap["duration"].(string); ok {
					stageTotal += scriptgen.ParseK6Duration(dur)
				}
			}
			if stageTotal > d {
				d = stageTotal
			}
		}

		if d > maxDuration {
			maxDuration = d
		}
	}

	return maxDuration
}

// durationRE matches k6 duration patterns in script source.
var durationRE = regexp.MustCompile(`duration:\s*['"](\d+[smh])['"]`)

// estimateFromScript tries to extract duration from k6 script source as a last resort.
func estimateFromScript(script string) int {
	matches := durationRE.FindAllStringSubmatch(script, -1)
	maxDuration := 0
	for _, m := range matches {
		if len(m) > 1 {
			d := scriptgen.ParseK6Duration(m[1])
			if d > maxDuration {
				maxDuration = d
			}
		}
	}
	return maxDuration
}
