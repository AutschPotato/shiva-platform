package handler

import (
	"testing"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
)

func TestApplyCompletionAuthSummaryMarksComplete(t *testing.T) {
	metadata := &model.TestMetadata{
		Auth: &model.AuthMetadata{
			Mode:         "oauth_client_credentials",
			SecretSource: "config_env",
		},
	}

	authSummary := &scriptgen.AuthSummaryData{
		Status:             "complete",
		Message:            "Authentication was enabled but no token request was required during this run.",
		Mode:               "oauth_client_credentials",
		TokenURL:           "https://auth.example.com/oauth/token",
		ClientAuthMethod:   "basic",
		RefreshSkewSeconds: 30,
		Metrics: model.AuthRuntimeMetrics{
			TokenRequestsTotal: 0,
			TokenReuseHitsTotal: 25,
			ResponseStatusCodes: []model.StatusCodeCount{
				{Code: 200, Count: 1},
			},
		},
	}

	var h TestHandler
	h.applyCompletionAuthSummary(metadata, authSummary)

	if metadata.Auth.MetricsStatus != "complete" {
		t.Fatalf("expected metrics status complete, got %q", metadata.Auth.MetricsStatus)
	}
	if metadata.Auth.MetricsMessage == "" {
		t.Fatalf("expected metrics message to be propagated")
	}
	if metadata.Auth.Metrics == nil {
		t.Fatalf("expected auth metrics to be set")
	}
	if metadata.Auth.Metrics.TokenReuseHitsTotal != 25 {
		t.Fatalf("expected reuse hits total 25, got %v", metadata.Auth.Metrics.TokenReuseHitsTotal)
	}
	if len(metadata.Auth.Metrics.ResponseStatusCodes) != 1 || metadata.Auth.Metrics.ResponseStatusCodes[0].Code != 200 {
		t.Fatalf("expected response status codes to be propagated, got %#v", metadata.Auth.Metrics.ResponseStatusCodes)
	}
}

func TestMarkAuthMetricsUnavailable(t *testing.T) {
	metadata := &model.TestMetadata{
		Auth: &model.AuthMetadata{
			Mode:         "oauth_client_credentials",
			SecretSource: "config_env",
		},
	}

	markAuthMetricsUnavailable(metadata)

	if metadata.Auth.MetricsStatus != "unavailable" {
		t.Fatalf("expected metrics status unavailable, got %q", metadata.Auth.MetricsStatus)
	}
	if metadata.Auth.MetricsMessage == "" {
		t.Fatalf("expected unavailable metrics message to be set")
	}
	if metadata.Auth.Metrics != nil {
		t.Fatalf("expected auth metrics to stay nil when unavailable")
	}
}

func TestApplyCompletionAuthSummaryPropagatesAbortContext(t *testing.T) {
	metadata := &model.TestMetadata{
		Auth: &model.AuthMetadata{
			Mode:         "oauth_client_credentials",
			SecretSource: "config_env",
		},
	}

	authSummary := &scriptgen.AuthSummaryData{
		Status:             "aborted",
		Message:            "token endpoint returned HTTP 401",
		Mode:               "oauth_client_credentials",
		TokenURL:           "https://auth.example.com/oauth/token",
		ClientAuthMethod:   "basic",
		RefreshSkewSeconds: 30,
		Metrics: model.AuthRuntimeMetrics{
			TokenRequestsTotal:   3,
			TokenFailureTotal:    3,
			AbortTriggered:       true,
			AbortCause:           "http_status",
			AbortReason:          "token endpoint returned HTTP 401",
			AbortHTTPStatusCodes: []int{401},
		},
	}

	var h TestHandler
	h.applyCompletionAuthSummary(metadata, authSummary)

	if metadata.Auth.MetricsStatus != "aborted" {
		t.Fatalf("expected metrics status aborted, got %q", metadata.Auth.MetricsStatus)
	}
	if metadata.Auth.Metrics == nil || !metadata.Auth.Metrics.AbortTriggered {
		t.Fatalf("expected auth abort context to be propagated")
	}
	if len(metadata.Auth.Metrics.AbortHTTPStatusCodes) != 1 || metadata.Auth.Metrics.AbortHTTPStatusCodes[0] != 401 {
		t.Fatalf("expected abort status code 401, got %#v", metadata.Auth.Metrics.AbortHTTPStatusCodes)
	}
}

func TestHydrateAuthMetadataFromRawSummaryBackfillsResponseCodes(t *testing.T) {
	rawSummary := `--- worker-1 ---
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
}`

	metadata := &model.TestMetadata{
		Auth: &model.AuthMetadata{
			Mode:         "oauth_client_credentials",
			SecretSource: "config_env",
		},
	}

	metadata = hydrateAuthMetadataFromRawSummary(metadata, rawSummary)
	if metadata == nil || metadata.Auth == nil || metadata.Auth.Metrics == nil {
		t.Fatalf("expected auth metadata to be hydrated from raw summary")
	}
	if len(metadata.Auth.Metrics.ResponseStatusCodes) != 1 || metadata.Auth.Metrics.ResponseStatusCodes[0].Code != 200 {
		t.Fatalf("expected response codes to be restored, got %#v", metadata.Auth.Metrics.ResponseStatusCodes)
	}
	if metadata.Auth.TokenURL != "https://auth.example.com/oauth/token" {
		t.Fatalf("expected token url to be hydrated, got %q", metadata.Auth.TokenURL)
	}
}
