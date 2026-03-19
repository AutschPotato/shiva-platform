package scriptgen

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
)

func TestBuildBuilderPayloadArtifactsExactKiBSize(t *testing.T) {
	req := &model.TestRequest{
		HTTPMethod:       "POST",
		ContentType:      "application/json",
		PayloadJSON:      `{"message":"hello"}`,
		PayloadTargetKiB: 1,
	}

	artifacts, err := BuildBuilderPayloadArtifacts(req)
	if err != nil {
		t.Fatalf("expected payload artifacts, got error: %v", err)
	}

	if artifacts.TargetBytes != 1024 {
		t.Fatalf("expected target bytes 1024, got %d", artifacts.TargetBytes)
	}
	if artifacts.ActualBytes != 1024 {
		t.Fatalf("expected actual bytes 1024, got %d", artifacts.ActualBytes)
	}
	if !strings.Contains(artifacts.Content, payloadPaddingField) {
		t.Fatalf("expected payload content to include padding field")
	}
}

func TestBuildBuilderPayloadArtifactsCountsUTF8Bytes(t *testing.T) {
	req := &model.TestRequest{
		HTTPMethod:  "POST",
		ContentType: "application/json",
		PayloadJSON: `{"message":"Grüße 👋"}`,
	}

	artifacts, err := BuildBuilderPayloadArtifacts(req)
	if err != nil {
		t.Fatalf("expected payload artifacts, got error: %v", err)
	}

	expectedBytes := len([]byte(`{"message":"Grüße 👋"}`))
	if artifacts.ActualBytes != expectedBytes {
		t.Fatalf("expected %d UTF-8 bytes, got %d", expectedBytes, artifacts.ActualBytes)
	}
}

func TestBuildBuilderPayloadArtifactsRejectsTooSmallTarget(t *testing.T) {
	if _, err := buildExactPayloadContent(map[string]any{"message": "hello"}, 1); err == nil {
		t.Fatalf("expected too-small target to fail")
	}
}

func TestGenerateFromBuilderIncludesAuthFlow(t *testing.T) {
	req := &model.TestRequest{
		URL:      "https://api.example.com/orders",
		Executor: "constant-vus",
		VUs:      1,
		Duration: "1m",
		Auth: model.AuthInput{
			Enabled:            true,
			Mode:               "oauth_client_credentials",
			TokenURL:           "https://auth.example.com/oauth/token",
			ClientID:           "demo-client",
			ClientAuthMethod:   "basic",
			RefreshSkewSeconds: 30,
		},
	}

	result, err := GenerateFromBuilder(req, 1)
	if err != nil {
		t.Fatalf("expected generated script, got error: %v", err)
	}

	if !strings.Contains(result.Script, `const BASE_URL = envString('`+TargetURLEnvVar+`', '');`) {
		t.Fatalf("expected target url to come from env contract")
	}
	if !strings.Contains(result.Script, `const HTTP_METHOD = envString('`+HTTPMethodEnvVar+`', '`+DefaultHTTPMethod+`').toUpperCase();`) {
		t.Fatalf("expected http method to come from env contract")
	}
	if !strings.Contains(result.Script, `const PAYLOAD_SOURCE_JSON = decodePayloadSourceJSON(`) {
		t.Fatalf("expected payload source json to be decoded via helper")
	}
	if !strings.Contains(result.Script, `encoding.b64decode(encodedValue, 'std', 's')`) {
		t.Fatalf("expected payload source json base64 decode support in generated script")
	}
	if !strings.Contains(result.Script, `const AUTH_ENABLED = envBool('`+AuthEnabledEnvVar+`', false);`) {
		t.Fatalf("expected auth enabled flag to come from env contract")
	}
	if !strings.Contains(result.Script, `const AUTH_TOKEN_URL = envString('`+AuthTokenURLEnvVar+`', '');`) {
		t.Fatalf("expected auth token url to come from env contract")
	}
	if !strings.Contains(result.Script, `const AUTH_CLIENT_SECRET = envString('`+AuthClientSecretEnvVar+`', '');`) {
		t.Fatalf("expected auth secret to come from env contract")
	}
	if !strings.Contains(result.Script, `const businessHttpRequests = new Counter('business_http_requests_total');`) {
		t.Fatalf("expected business request counter in generated script")
	}
	if !strings.Contains(result.Script, `const businessHttpDuration = new Trend('business_http_duration_ms', true);`) {
		t.Fatalf("expected business duration trend in generated script")
	}
	if !strings.Contains(result.Script, `businessHttpRequests.add(1);`) {
		t.Fatalf("expected business request counter to be incremented")
	}
	if !strings.Contains(result.Script, `businessHttpWaiting.add(Number(res.timings.waiting || 0));`) {
		t.Fatalf("expected business latency breakdown timings in generated script")
	}
	if !strings.Contains(result.Script, `function ensureAuthorizationHeader()`) {
		t.Fatalf("expected ensureAuthorizationHeader helper in generated script")
	}
	if !strings.Contains(result.Script, `const AUTH_TOKEN_TIMEOUT = envString('`+AuthTokenTimeoutEnvVar+`', '10s');`) {
		t.Fatalf("expected auth token timeout to come from env contract")
	}
	if !strings.Contains(result.Script, `const AUTH_RETRYABLE_STATUS_CODES = envString('`+AuthRetryableStatusCodesEnvVar+`', '408,429,502,503,504');`) {
		t.Fatalf("expected auth retryable status codes to come from env contract")
	}
	if !strings.Contains(result.Script, `const authTokenResponses = new Counter('auth_token_responses_total');`) {
		t.Fatalf("expected auth response counter in generated script")
	}
	if !strings.Contains(result.Script, `const authAbortEvents = new Counter('auth_abort_events_total');`) {
		t.Fatalf("expected auth abort counter in generated script")
	}
	if !strings.Contains(result.Script, `function retryDelaySeconds(res)`) {
		t.Fatalf("expected retryDelaySeconds helper in generated script")
	}
	if !strings.Contains(result.Script, `authTokenResponses.add(1, { status_code: String(res.status) });`) {
		t.Fatalf("expected auth response status tagging in generated script")
	}
	if !strings.Contains(result.Script, `authAbortEvents.add(1, tags);`) {
		t.Fatalf("expected auth abort tagging in generated script")
	}
	if !strings.Contains(result.Script, `exec.test.abort('Authentication aborted test run: ' + String(reason || 'token request failed'));`) {
		t.Fatalf("expected auth failures to abort the test run")
	}
	if !strings.Contains(InjectSummaryExport(result.Script), `const authAbortSummaryFromMetrics = () => {`) {
		t.Fatalf("expected auth abort summary derivation in handleSummary")
	}
	if !strings.Contains(InjectSummaryExport(result.Script), `part.indexOf('=')`) {
		t.Fatalf("expected tagged summary parsing to support equals separators")
	}
	if !strings.Contains(InjectSummaryExport(result.Script), `/output/auth-summary-`) {
		t.Fatalf("expected auth summary artifact in handleSummary")
	}
}

func TestBuildBuilderConfigIncludesVisibleRuntimeContract(t *testing.T) {
	req := &model.TestRequest{
		URL:              "https://api.example.com/orders",
		Executor:         "constant-vus",
		VUs:              3,
		Duration:         "2m",
		HTTPMethod:       "POST",
		ContentType:      "application/json",
		PayloadJSON:      `{"hello":"world"}`,
		PayloadTargetKiB: 1,
		Auth: model.AuthInput{
			Enabled:            true,
			Mode:               "oauth_client_credentials",
			TokenURL:           "https://auth.example.com/oauth/token",
			ClientID:           "demo-client",
			ClientSecret:       "demo-secret",
			ClientAuthMethod:   "basic",
			RefreshSkewSeconds: 45,
		},
	}

	content, err := BuildBuilderConfig(req)
	if err != nil {
		t.Fatalf("expected builder config, got error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("expected valid config json, got error: %v", err)
	}

	env, ok := config["env"].(map[string]any)
	if !ok {
		t.Fatalf("expected env block in builder config")
	}

	assertEnvValue := func(key string, expected string) {
		t.Helper()
		if got := env[key]; got != expected {
			t.Fatalf("expected env %s=%q, got %#v", key, expected, got)
		}
	}

	assertEnvValue(TargetURLEnvVar, "https://api.example.com/orders")
	assertEnvValue(HTTPMethodEnvVar, "POST")
	assertEnvValue(ContentTypeEnvVar, "application/json")
	assertEnvValue(PayloadSourceJSONEnvVar, `{"hello":"world"}`)
	assertEnvValue(PayloadTargetBytesEnvVar, "1024")
	assertEnvValue(AuthEnabledEnvVar, "true")
	assertEnvValue(AuthTokenURLEnvVar, "https://auth.example.com/oauth/token")
	assertEnvValue(AuthClientIDEnvVar, "demo-client")
	assertEnvValue(AuthClientSecretEnvVar, "demo-secret")
	assertEnvValue(AuthClientAuthMethodEnvVar, "basic")
	assertEnvValue(AuthRefreshSkewEnvVar, "45")
	assertEnvValue(AuthRetryableStatusCodesEnvVar, "408,429,502,503,504")
}

func TestEnrichBuilderConfigMergesBuilderEnvContract(t *testing.T) {
	req := &model.TestRequest{
		URL:        "https://api.example.com/reuse",
		HTTPMethod: "PATCH",
		Auth: model.AuthInput{
			Enabled:      true,
			TokenURL:     "https://auth.example.com/oauth/token",
			ClientID:     "reuse-client",
			ClientSecret: "reuse-secret",
		},
	}

	content, err := EnrichBuilderConfig(`{"env":{"EXISTING":"value"}}`, req)
	if err != nil {
		t.Fatalf("expected config enrichment, got error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("expected valid config json, got error: %v", err)
	}

	env, ok := config["env"].(map[string]any)
	if !ok {
		t.Fatalf("expected env block in config")
	}

	if env["EXISTING"] != "value" {
		t.Fatalf("expected existing env value to be preserved")
	}
	if env[TargetURLEnvVar] != "https://api.example.com/reuse" {
		t.Fatalf("expected target url to be injected into config env")
	}
	if env[HTTPMethodEnvVar] != "PATCH" {
		t.Fatalf("expected http method to be injected into config env")
	}
	if env[AuthClientSecretEnvVar] != "reuse-secret" {
		t.Fatalf("expected auth client secret to be visible in config env")
	}
}

func TestWriteEnvFileEncodesPayloadJSONAsBase64(t *testing.T) {
	scriptsDir := t.TempDir()
	payload := "{\n  \"message\": \"Lorem ipsum dolor sit amet\"\n}"

	err := WriteEnvFile(scriptsDir, map[string]string{
		TargetURLEnvVar:         "http://target-lb:8090",
		PayloadSourceJSONEnvVar: payload,
	})
	if err != nil {
		t.Fatalf("expected env file to be written, got error: %v", err)
	}

	contentBytes, err := os.ReadFile(filepath.Join(scriptsDir, "k6-env.sh"))
	if err != nil {
		t.Fatalf("failed to read env file: %v", err)
	}
	content := string(contentBytes)

	if strings.Contains(content, PayloadSourceJSONEnvVar+"='") {
		t.Fatalf("expected raw payload json env to be omitted from shell env file")
	}
	if !strings.Contains(content, PayloadSourceJSONB64EnvVar+"='") {
		t.Fatalf("expected base64 payload env entry in shell env file")
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	if !strings.Contains(content, encoded) {
		t.Fatalf("expected encoded payload content in shell env file")
	}
}

func TestGenerateFromBuilderDoesNotApplyDefaultThinkTimeToArrivalRateExecutors(t *testing.T) {
	req := &model.TestRequest{
		URL:      "https://api.example.com/orders",
		Executor: "constant-arrival-rate",
		Rate:     6000,
		TimeUnit: "1s",
		Duration: "1m",
	}

	result, err := GenerateFromBuilder(req, 10)
	if err != nil {
		t.Fatalf("expected generated script, got error: %v", err)
	}

	if strings.Contains(result.Script, "sleep(0.5);") {
		t.Fatalf("expected no default think-time in arrival-rate script")
	}
}

func TestGenerateFromBuilderKeepsDefaultThinkTimeForVUExecutors(t *testing.T) {
	req := &model.TestRequest{
		URL:      "https://api.example.com/orders",
		Executor: "constant-vus",
		VUs:      1,
		Duration: "1m",
	}

	result, err := GenerateFromBuilder(req, 1)
	if err != nil {
		t.Fatalf("expected generated script, got error: %v", err)
	}

	if !strings.Contains(result.Script, "sleep(0.5);") {
		t.Fatalf("expected default think-time to remain for VU-based executors")
	}
}

func TestEstimateConfiguredExecutionDurationForConstantArrivalRate(t *testing.T) {
	config := `{"scenarios":{"default":{"executor":"constant-arrival-rate","rate":1000,"timeUnit":"1s","duration":"1m","preAllocatedVUs":20,"maxVUs":40}}}`
	if got := EstimateConfiguredExecutionDuration(config); got != time.Minute {
		t.Fatalf("expected 1m configured duration, got %s", got)
	}
}
