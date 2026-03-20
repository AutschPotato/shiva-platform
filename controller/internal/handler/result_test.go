package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/shiva-load-testing/controller/internal/model"
)

func TestParseResultListParamsAcceptsQAlias(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/result/list?limit=8&offset=16&q=playwright_native_arrival", nil)

	limit, offset, search := parseResultListParams(req)
	if limit != 8 {
		t.Fatalf("expected limit 8, got %d", limit)
	}
	if offset != 16 {
		t.Fatalf("expected offset 16, got %d", offset)
	}
	if search != "playwright_native_arrival" {
		t.Fatalf("expected q alias to be used as search, got %q", search)
	}
}

func TestParseResultListParamsPrefersExplicitSearch(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/result/list?search=direct&q=ignored", nil)

	_, _, search := parseResultListParams(req)
	if search != "direct" {
		t.Fatalf("expected explicit search param to win, got %q", search)
	}
}

func TestBuildResultResponseIncludesExecutionFieldsForBuilderRuns(t *testing.T) {
	sleepSeconds := 1.25
	resp := buildResultResponse(&model.LoadTest{
		ID:              "result-1",
		ProjectName:     "native-fixed-throughput",
		URL:             "http://target-lb:8090/health",
		Status:          "completed",
		UserID:          7,
		Username:        "admin",
		Executor:        "constant-arrival-rate",
		Rate:            240,
		Duration:        "30s",
		TimeUnit:        "1s",
		PreAllocatedVUs: 12,
		MaxVUs:          24,
		SleepSeconds:    &sleepSeconds,
	})

	if got := resp["executor"]; got != "constant-arrival-rate" {
		t.Fatalf("expected executor to round-trip, got %#v", got)
	}
	if got := resp["rate"]; got != 240 {
		t.Fatalf("expected rate 240, got %#v", got)
	}
	if got := resp["duration"]; got != "30s" {
		t.Fatalf("expected duration 30s, got %#v", got)
	}
	if got := resp["time_unit"]; got != "1s" {
		t.Fatalf("expected time_unit 1s, got %#v", got)
	}
	if got := resp["pre_allocated_vus"]; got != 12 {
		t.Fatalf("expected pre_allocated_vus 12, got %#v", got)
	}
	if got := resp["max_vus"]; got != 24 {
		t.Fatalf("expected max_vus 24, got %#v", got)
	}
	if got := resp["sleep_seconds"]; got != sleepSeconds {
		t.Fatalf("expected sleep_seconds %.2f, got %#v", sleepSeconds, got)
	}
}

func TestBuildResultResponseOmitsExecutionFieldsWhenOnlyDefaultsExist(t *testing.T) {
	resp := buildResultResponse(&model.LoadTest{
		ID:            "result-2",
		ProjectName:   "upload-run",
		URL:           "",
		Status:        "completed",
		UserID:        7,
		Username:      "admin",
		Executor:      "ramping-vus",
		TimeUnit:      "1s",
		ScriptContent: "export default function() {}",
	})

	for _, key := range []string{"executor", "stages", "vus", "duration", "rate", "time_unit", "pre_allocated_vus", "max_vus", "sleep_seconds"} {
		if _, ok := resp[key]; ok {
			t.Fatalf("expected %s to be omitted for non-builder/default-only result payload", key)
		}
	}
}
