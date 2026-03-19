package handler

import (
	"testing"

	"github.com/shiva-load-testing/controller/internal/model"
)

func TestBuildMetricsV2UsesBusinessSummaryAndAuthSplit(t *testing.T) {
	legacy := &model.AggregatedMetrics{
		TotalRequests: 150,
		HTTPSuccesses: 140,
		HTTPFailures:  10,
		SuccessRate:   140.0 / 150.0,
		ErrorRate:     10.0 / 150.0,
		RPS:           15,
		Iterations:    140,
		DataReceived:  2048,
		DataSent:      1024,
		Status4xx:     5,
		Status5xx:     2,
		Thresholds: []model.ThresholdResult{
			{Metric: "http_req_duration", Passed: true},
		},
	}

	rawSummary := `--- worker-1 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 175, "rate": 17.5}},
    "http_req_failed": {"values": {"rate": 0.04}},
    "iterations": {"values": {"count": 165}},
    "checks": {"values": {"passes": 140, "fails": 10}},
    "http_req_duration": {"values": {"avg": 120, "med": 110, "min": 50, "max": 300, "p(90)": 180, "p(95)": 220, "p(99)": 260}},
    "business_http_requests_total": {"values": {"count": 140}},
    "business_http_success_total": {"values": {"count": 132}},
    "business_http_failure_total": {"values": {"count": 8}},
    "business_status_2xx": {"values": {"count": 132}},
    "business_status_4xx": {"values": {"count": 5}},
    "business_status_5xx": {"values": {"count": 2}},
    "business_transport_failures_total": {"values": {"count": 1}},
    "business_http_duration_ms": {"values": {"avg": 100, "med": 95, "min": 40, "max": 200, "p(90)": 150, "p(95)": 170, "p(99)": 195}}
  },
  "state": {"testRunDurationMs": 10000}
}`

	metadata := &model.TestMetadata{
		DurationS: 10,
		Auth: &model.AuthMetadata{
			Metrics: &model.AuthRuntimeMetrics{
				TokenRequestsTotal:  10,
				TokenSuccessTotal:   9,
				TokenFailureTotal:   1,
				TokenSuccessRate:    0.9,
				TokenRequestAvgMs:   50,
				TokenRequestP95Ms:   60,
				TokenRequestP99Ms:   70,
				TokenRequestMaxMs:   70,
				TokenRefreshTotal:   2,
				TokenReuseHitsTotal: 100,
				ResponseStatusCodes: []model.StatusCodeCount{
					{Code: 200, Count: 10},
				},
			},
		},
	}

	metrics := buildMetricsV2(legacy, rawSummary, metadata)
	if metrics == nil {
		t.Fatalf("expected metrics_v2")
	}
	if metrics.HTTPBusiness.Requests != 140 {
		t.Fatalf("expected 140 business requests, got %v", metrics.HTTPBusiness.Requests)
	}
	if metrics.HTTPTotal.Requests != 175 {
		t.Fatalf("expected summary total requests 175, got %v", metrics.HTTPTotal.Requests)
	}
	if metrics.Iterations.Count != 165 {
		t.Fatalf("expected summary iterations 165, got %v", metrics.Iterations.Count)
	}
	if metrics.HTTPTotal.Failures != 7 {
		t.Fatalf("expected derived total failures 7, got %v", metrics.HTTPTotal.Failures)
	}
	if metrics.HTTPAuxiliary.Requests != 35 {
		t.Fatalf("expected 35 auxiliary HTTP requests, got %v", metrics.HTTPAuxiliary.Requests)
	}
	if metrics.PrimaryLatency.Scope != "http_business" {
		t.Fatalf("expected business latency scope, got %q", metrics.PrimaryLatency.Scope)
	}
	if len(metrics.Workers) != 1 {
		t.Fatalf("expected one worker entry, got %d", len(metrics.Workers))
	}
	if metrics.Workers[0].BusinessRequests != 140 {
		t.Fatalf("expected worker business requests")
	}
	if metrics.Workers[0].AuxiliaryRequests != 35 {
		t.Fatalf("expected worker auxiliary requests to be derived from total-business, got %v", metrics.Workers[0].AuxiliaryRequests)
	}
	if len(metrics.QualityFlags) == 0 {
		t.Fatalf("expected quality flags to be populated")
	}
}

func TestBuildMetricsV2DerivesZeroBusinessRequestsForAuthOnlyRun(t *testing.T) {
	legacy := &model.AggregatedMetrics{
		TotalRequests: 0,
		HTTPSuccesses: 0,
		HTTPFailures:  0,
	}

	rawSummary := `--- worker-1 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 10}},
    "http_req_failed": {"values": {"rate": 1}},
    "iterations": {"values": {"count": 10}},
    "auth_token_requests_total": {"values": {"count": 10}},
    "auth_token_failure_total": {"values": {"count": 10}},
    "auth_token_request_duration_ms": {"values": {"avg": 3, "p(95)": 4, "p(99)": 5, "max": 5}}
  },
  "state": {"testRunDurationMs": 6000}
}`

	metadata := &model.TestMetadata{
		DurationS: 6,
		Auth: &model.AuthMetadata{
			Metrics: &model.AuthRuntimeMetrics{
				TokenRequestsTotal: 10,
				TokenFailureTotal:  10,
			},
		},
	}

	metrics := buildMetricsV2(legacy, rawSummary, metadata)
	if metrics == nil {
		t.Fatalf("expected metrics_v2")
	}
	if metrics.HTTPTotal.Requests != 10 {
		t.Fatalf("expected total requests 10, got %v", metrics.HTTPTotal.Requests)
	}
	if metrics.HTTPAuxiliary.Requests != 10 {
		t.Fatalf("expected auxiliary requests 10, got %v", metrics.HTTPAuxiliary.Requests)
	}
	if metrics.HTTPBusiness.Requests != 0 {
		t.Fatalf("expected business requests 0 for auth-only run, got %v", metrics.HTTPBusiness.Requests)
	}
}
