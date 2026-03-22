package scriptgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAndMergeAuthSummariesAggregatesWorkerMetrics(t *testing.T) {
	dir := t.TempDir()

	workerOne := `{
  "mode": "oauth_client_credentials",
  "token_url": "https://auth.example.com/oauth/token",
  "client_auth_method": "basic",
  "refresh_skew_seconds": 30,
  "metrics": {
    "token_requests_total": 2,
    "token_success_total": 2,
    "token_failure_total": 0,
    "token_success_rate": 1,
    "token_request_avg_ms": 120,
    "token_request_p95_ms": 140,
    "token_request_p99_ms": 150,
    "token_request_max_ms": 150,
    "token_refresh_total": 1,
    "token_reuse_hits_total": 8,
    "response_status_codes": [{"code": 200, "count": 2}]
  }
}`
	workerTwo := `{
  "status": "aborted",
  "message": "token endpoint returned HTTP 401",
  "mode": "oauth_client_credentials",
  "token_url": "https://auth.example.com/oauth/token",
  "client_auth_method": "basic",
  "refresh_skew_seconds": 30,
  "metrics": {
    "token_requests_total": 1,
    "token_success_total": 0,
    "token_failure_total": 1,
    "token_success_rate": 0,
    "token_request_avg_ms": 300,
    "token_request_p95_ms": 320,
    "token_request_p99_ms": 340,
    "token_request_max_ms": 340,
    "token_refresh_total": 0,
    "token_reuse_hits_total": 2,
    "response_status_codes": [{"code": 401, "count": 1}],
    "abort_triggered": true,
    "abort_cause": "http_status",
    "abort_reason": "token endpoint returned HTTP 401",
    "abort_http_status_codes": [401],
    "abort_retryable": false
  }
}`

	if err := os.WriteFile(filepath.Join(dir, "auth-summary-worker-1.json"), []byte(workerOne), 0644); err != nil {
		t.Fatalf("write worker one: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth-summary-worker-2.json"), []byte(workerTwo), 0644); err != nil {
		t.Fatalf("write worker two: %v", err)
	}

	summary, err := ReadAndMergeAuthSummaries(dir)
	if err != nil {
		t.Fatalf("expected merged auth summary, got error: %v", err)
	}

	if summary.Mode != "oauth_client_credentials" {
		t.Fatalf("expected mode oauth_client_credentials, got %q", summary.Mode)
	}
	if summary.Status != "aborted" {
		t.Fatalf("expected auth summary status aborted, got %q", summary.Status)
	}
	if summary.Metrics.TokenRequestsTotal != 3 {
		t.Fatalf("expected 3 token requests, got %v", summary.Metrics.TokenRequestsTotal)
	}
	if summary.Metrics.TokenSuccessTotal != 2 {
		t.Fatalf("expected 2 token successes, got %v", summary.Metrics.TokenSuccessTotal)
	}
	if summary.Metrics.TokenFailureTotal != 1 {
		t.Fatalf("expected 1 token failure, got %v", summary.Metrics.TokenFailureTotal)
	}
	if summary.Metrics.TokenSuccessRate != 2.0/3.0 {
		t.Fatalf("expected success rate 2/3, got %v", summary.Metrics.TokenSuccessRate)
	}
	if summary.Metrics.TokenRefreshTotal != 1 {
		t.Fatalf("expected refresh total 1, got %v", summary.Metrics.TokenRefreshTotal)
	}
	if summary.Metrics.TokenReuseHitsTotal != 10 {
		t.Fatalf("expected reuse hits total 10, got %v", summary.Metrics.TokenReuseHitsTotal)
	}
	if len(summary.Metrics.ResponseStatusCodes) != 2 {
		t.Fatalf("expected 2 response status code entries, got %#v", summary.Metrics.ResponseStatusCodes)
	}
	if summary.Metrics.ResponseStatusCodes[0].Code != 200 || summary.Metrics.ResponseStatusCodes[0].Count != 2 {
		t.Fatalf("expected 200 x2 response status code entry, got %#v", summary.Metrics.ResponseStatusCodes[0])
	}
	if summary.Metrics.ResponseStatusCodes[1].Code != 401 || summary.Metrics.ResponseStatusCodes[1].Count != 1 {
		t.Fatalf("expected 401 x1 response status code entry, got %#v", summary.Metrics.ResponseStatusCodes[1])
	}
	if summary.Metrics.TokenRequestAvgMs <= 0 {
		t.Fatalf("expected weighted avg duration to be set")
	}
	if summary.Metrics.TokenRequestMaxMs != 340 {
		t.Fatalf("expected max duration 340, got %v", summary.Metrics.TokenRequestMaxMs)
	}
	if !summary.Metrics.AbortTriggered {
		t.Fatalf("expected merged auth summary to mark abort")
	}
	if summary.Metrics.AbortCause != "http_status" {
		t.Fatalf("expected abort cause http_status, got %q", summary.Metrics.AbortCause)
	}
	if len(summary.Metrics.AbortHTTPStatusCodes) != 1 || summary.Metrics.AbortHTTPStatusCodes[0] != 401 {
		t.Fatalf("expected abort status code 401, got %#v", summary.Metrics.AbortHTTPStatusCodes)
	}
	if summary.Message != "token endpoint returned HTTP 401" {
		t.Fatalf("expected abort message to be propagated, got %q", summary.Message)
	}

	raw := ReadRawAuthSummaries(dir)
	if !strings.Contains(raw, "--- worker-1 ---") {
		t.Fatalf("expected raw auth summary to include worker name")
	}
}

func TestParseRawAuthSummaryContentBuildsMergedStatusCodes(t *testing.T) {
	content := `--- worker-1 ---
{
  "mode": "oauth_client_credentials",
  "token_url": "https://auth.example.com/oauth/token",
  "client_auth_method": "basic",
  "refresh_skew_seconds": 30,
  "metrics": {
    "token_requests_total": 2,
    "token_success_total": 2,
    "token_failure_total": 0,
    "token_success_rate": 1,
    "token_request_avg_ms": 120,
    "token_request_p95_ms": 140,
    "token_request_p99_ms": 150,
    "token_request_max_ms": 150,
    "token_refresh_total": 1,
    "token_reuse_hits_total": 8,
    "response_status_codes": [{"code": 200, "count": 2}]
  }
}

--- worker-2 ---
{
  "status": "aborted",
  "message": "token endpoint returned HTTP 401",
  "mode": "oauth_client_credentials",
  "token_url": "https://auth.example.com/oauth/token",
  "client_auth_method": "basic",
  "refresh_skew_seconds": 30,
  "metrics": {
    "token_requests_total": 1,
    "token_success_total": 0,
    "token_failure_total": 1,
    "token_success_rate": 0,
    "token_request_avg_ms": 300,
    "token_request_p95_ms": 320,
    "token_request_p99_ms": 340,
    "token_request_max_ms": 340,
    "token_refresh_total": 0,
    "token_reuse_hits_total": 2,
    "response_status_codes": [{"code": 401, "count": 1}],
    "abort_triggered": true,
    "abort_cause": "http_status",
    "abort_reason": "token endpoint returned HTTP 401",
    "abort_http_status_codes": [401],
    "abort_retryable": false
  }
}`

	summary, err := ParseRawAuthSummaryContent(content)
	if err != nil {
		t.Fatalf("expected parsed auth summary, got error: %v", err)
	}
	if summary.Metrics.TokenRequestsTotal != 3 {
		t.Fatalf("expected 3 token requests, got %v", summary.Metrics.TokenRequestsTotal)
	}
	if len(summary.Metrics.ResponseStatusCodes) != 2 {
		t.Fatalf("expected merged response status codes, got %#v", summary.Metrics.ResponseStatusCodes)
	}
	if summary.Metrics.ResponseStatusCodes[0].Code != 200 || summary.Metrics.ResponseStatusCodes[1].Code != 401 {
		t.Fatalf("expected merged 200/401 status codes, got %#v", summary.Metrics.ResponseStatusCodes)
	}
	if !summary.Metrics.AbortTriggered {
		t.Fatalf("expected abort flag to be propagated")
	}
}

func TestParseRawSummaryContentBuildsBusinessMetrics(t *testing.T) {
	content := `--- worker-1 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 100, "rate": 10}},
    "http_req_failed": {"values": {"rate": 0}},
    "iterations": {"values": {"count": 100}},
    "checks": {"values": {"passes": 100, "fails": 0}},
    "http_req_duration": {"values": {"avg": 120, "med": 110, "min": 50, "max": 300, "p(90)": 180, "p(95)": 220, "p(99)": 260}},
    "business_http_requests_total": {"values": {"count": 95}},
    "business_http_success_total": {"values": {"count": 92}},
    "business_http_failure_total": {"values": {"count": 3}},
    "business_status_2xx": {"values": {"count": 92}},
    "business_status_4xx": {"values": {"count": 2}},
    "business_status_5xx": {"values": {"count": 1}},
    "business_transport_failures_total": {"values": {"count": 0}},
    "business_http_duration_ms": {"values": {"avg": 100, "med": 95, "min": 40, "max": 200, "p(90)": 150, "p(95)": 170, "p(99)": 195}},
    "business_http_waiting_ms": {"values": {"avg": 80, "max": 140, "p(95)": 120, "p(99)": 135}},
    "business_http_sending_ms": {"values": {"avg": 3, "max": 8, "p(95)": 5, "p(99)": 7}},
    "business_http_receiving_ms": {"values": {"avg": 5, "max": 12, "p(95)": 9, "p(99)": 11}},
    "business_http_blocked_ms": {"values": {"avg": 6, "max": 20, "p(95)": 12, "p(99)": 18}},
    "business_http_connecting_ms": {"values": {"avg": 2, "max": 6, "p(95)": 4, "p(99)": 5}},
    "business_http_tls_handshaking_ms": {"values": {"avg": 1, "max": 3, "p(95)": 2, "p(99)": 2}},
    "auth_token_requests_total": {"values": {"count": 5}}
  },
  "state": {"testRunDurationMs": 10000}
}

--- worker-2 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 50, "rate": 5}},
    "http_req_failed": {"values": {"rate": 0.04}},
    "iterations": {"values": {"count": 50}},
    "checks": {"values": {"passes": 48, "fails": 2}},
    "http_req_duration": {"values": {"avg": 200, "med": 190, "min": 90, "max": 400, "p(90)": 280, "p(95)": 320, "p(99)": 360}},
    "business_http_requests_total": {"values": {"count": 45}},
    "business_http_success_total": {"values": {"count": 40}},
    "business_http_failure_total": {"values": {"count": 5}},
    "business_status_2xx": {"values": {"count": 40}},
    "business_status_4xx": {"values": {"count": 3}},
    "business_status_5xx": {"values": {"count": 1}},
    "business_transport_failures_total": {"values": {"count": 1}},
    "business_http_duration_ms": {"values": {"avg": 180, "med": 170, "min": 80, "max": 350, "p(90)": 260, "p(95)": 300, "p(99)": 330}},
    "auth_token_requests_total": {"values": {"count": 2}}
  },
  "state": {"testRunDurationMs": 10000}
}`

	merged, err := ParseRawSummaryContent(content)
	if err != nil {
		t.Fatalf("expected merged summary, got error: %v", err)
	}

	if merged.TotalRequests != 150 {
		t.Fatalf("expected 150 total requests, got %v", merged.TotalRequests)
	}
	if merged.TotalSuccesses != 148 {
		t.Fatalf("expected 148 total successes, got %v", merged.TotalSuccesses)
	}
	if merged.TotalFailures != 2 {
		t.Fatalf("expected 2 total failures, got %v", merged.TotalFailures)
	}
	if merged.TotalStatus4xx != 0 {
		t.Fatalf("expected 0 total 4xx, got %v", merged.TotalStatus4xx)
	}
	if merged.TotalStatus5xx != 0 {
		t.Fatalf("expected 0 total 5xx, got %v", merged.TotalStatus5xx)
	}
	if merged.BusinessRequests != 140 {
		t.Fatalf("expected 140 business requests, got %v", merged.BusinessRequests)
	}
	if merged.BusinessSuccesses != 132 {
		t.Fatalf("expected 132 business successes, got %v", merged.BusinessSuccesses)
	}
	if merged.BusinessFailures != 8 {
		t.Fatalf("expected 8 business failures, got %v", merged.BusinessFailures)
	}
	if merged.BusinessBreakdown == nil {
		t.Fatalf("expected business breakdown to be present")
	}
	if len(merged.Workers) != 2 {
		t.Fatalf("expected 2 worker summaries, got %d", len(merged.Workers))
	}
	if merged.Workers[0].AuthRequests != 5 {
		t.Fatalf("expected first worker auth requests to be captured")
	}
}

func TestParseRawSummaryContentIgnoresZeroBusinessDurationMin(t *testing.T) {
	content := `--- worker-1 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 10}},
    "business_http_requests_total": {"values": {"count": 10}},
    "business_http_duration_ms": {"values": {"avg": 10, "med": 9, "min": 0, "max": 20, "p(90)": 15, "p(95)": 17, "p(99)": 19}}
  }
}

--- worker-2 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 10}},
    "business_http_requests_total": {"values": {"count": 10}},
    "business_http_duration_ms": {"values": {"avg": 12, "med": 11, "min": 5, "max": 25, "p(90)": 18, "p(95)": 20, "p(99)": 23}}
  }
}`

	merged, err := ParseRawSummaryContent(content)
	if err != nil {
		t.Fatalf("expected merged summary, got error: %v", err)
	}
	if merged.BusinessLatency.Min != 5 {
		t.Fatalf("expected positive minimum latency 5, got %v", merged.BusinessLatency.Min)
	}
}

func TestParseRawSummaryContentPreservesFirstWorkerName(t *testing.T) {
	content := `--- worker1 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 10}},
    "http_req_duration": {"values": {"avg": 10, "med": 9, "min": 1, "max": 20, "p(90)": 15, "p(95)": 17, "p(99)": 19}}
  },
  "state": {"testRunDurationMs": 0}
}

--- worker2 ---
{
  "metrics": {
    "http_reqs": {"values": {"count": 20}},
    "http_req_duration": {"values": {"avg": 20, "med": 19, "min": 2, "max": 30, "p(90)": 25, "p(95)": 27, "p(99)": 29}}
  },
  "state": {"testRunDurationMs": 0}
}`

	merged, err := ParseRawSummaryContent(content)
	if err != nil {
		t.Fatalf("expected merged summary, got error: %v", err)
	}
	if len(merged.Workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(merged.Workers))
	}
	if merged.Workers[0].Name != "worker1" {
		t.Fatalf("expected first worker name worker1, got %q", merged.Workers[0].Name)
	}
	if merged.Workers[1].Name != "worker2" {
		t.Fatalf("expected second worker name worker2, got %q", merged.Workers[1].Name)
	}
}
