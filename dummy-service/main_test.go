package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHandleTokenWithBasicAuthReturnsJWT(t *testing.T) {
	cfg = Config{
		AuthClientID:     "dummy-client",
		AuthClientSecret: "dummy-secret",
		AuthJWTSecret:    "dummy-jwt-secret",
		AuthIssuer:       "dummy-service",
		AuthTokenTTL:     60 * time.Second,
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("dummy-client:dummy-secret")))

	rec := httptest.NewRecorder()
	handleToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d with body %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	token, _ := payload["access_token"].(string)
	if token == "" {
		t.Fatalf("expected access_token in response")
	}

	claims, err := parseAndValidateDummyJWT(token, time.Now().UTC())
	if err != nil {
		t.Fatalf("expected valid dummy JWT, got error: %v", err)
	}
	if claims.Sub != "dummy-client" {
		t.Fatalf("expected subject dummy-client, got %q", claims.Sub)
	}
}

func TestHandleTokenRejectsInvalidCredentials(t *testing.T) {
	cfg = Config{
		AuthClientID:     "dummy-client",
		AuthClientSecret: "dummy-secret",
		AuthJWTSecret:    "dummy-jwt-secret",
		AuthIssuer:       "dummy-service",
		AuthTokenTTL:     60 * time.Second,
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "dummy-client")
	form.Set("client_secret", "wrong-secret")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	handleToken(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d with body %s", rec.Code, rec.Body.String())
	}
}

func TestValidateBearerTokenRejectsExpiredToken(t *testing.T) {
	cfg = Config{
		AuthJWTSecret: "dummy-jwt-secret",
		AuthIssuer:    "dummy-service",
		AuthTokenTTL:  1 * time.Second,
	}

	token, _, err := issueDummyJWT("dummy-client", time.Now().UTC().Add(-5*time.Second))
	if err != nil {
		t.Fatalf("failed to issue dummy JWT: %v", err)
	}

	err = validateBearerToken(nil, "Bearer "+token)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired token error, got %v", err)
	}
}

func TestHandleHTTPScenarioReturnsRequestedStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test/http/404", nil)
	req.SetPathValue("scenario", "404")

	rec := httptest.NewRecorder()
	handleHTTPScenario(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload["scenario"] != "404" {
		t.Fatalf("expected scenario marker 404, got %v", payload["scenario"])
	}
	if payload["code"] != float64(http.StatusNotFound) {
		t.Fatalf("expected code field %d, got %v", http.StatusNotFound, payload["code"])
	}
	if payload["method"] != http.MethodGet {
		t.Fatalf("expected method marker %s, got %v", http.MethodGet, payload["method"])
	}
}

func TestHandleHTTPScenarioReturnsRequestedStatusForPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/test/http/500", strings.NewReader(`{"hello":"world"}`))
	req.SetPathValue("scenario", "500")

	rec := httptest.NewRecorder()
	handleHTTPScenario(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleHTTPScenarioSupportsAllConfiguredMethods(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range methods {
		method := method
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/test/http/404", strings.NewReader(`{"hello":"world"}`))
			req.SetPathValue("scenario", "404")

			rec := httptest.NewRecorder()
			handleHTTPScenario(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404 for method %s, got %d", method, rec.Code)
			}

			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("failed to decode response payload for method %s: %v", method, err)
			}

			if payload["method"] != method {
				t.Fatalf("expected method marker %s, got %v", method, payload["method"])
			}
		})
	}
}

func TestHandleHTTPScenarioRejectsInvalidScenarioValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test/http/not-a-code", nil)
	req.SetPathValue("scenario", "not-a-code")

	rec := httptest.NewRecorder()
	handleHTTPScenario(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid scenario, got %d", rec.Code)
	}
}

func TestHandleHTTPScenarioSetsRedirectLocationFor3xx(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test/http/302", nil)
	req.SetPathValue("scenario", "302")

	rec := httptest.NewRecorder()
	handleHTTPScenario(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/api/products" {
		t.Fatalf("expected Location header /api/products, got %q", got)
	}
}

func TestHandleHTTPScenarioHonorsAuthRequirement(t *testing.T) {
	previousCfg := cfg
	t.Cleanup(func() { cfg = previousCfg })

	cfg = Config{
		RequireAuth:   true,
		AuthJWTSecret: "dummy-jwt-secret",
		AuthIssuer:    "dummy-service",
		AuthTokenTTL:  60 * time.Second,
	}

	req := httptest.NewRequest(http.MethodGet, "/test/http/404", nil)
	req.SetPathValue("scenario", "404")

	rec := httptest.NewRecorder()
	handleHTTPScenario(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when auth is required and Authorization header is missing, got %d", rec.Code)
	}
}

func TestHandleTokenScenarioReturnsRequestedStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token/503", nil)
	req.SetPathValue("scenario", "503")

	rec := httptest.NewRecorder()
	handleTokenScenario(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleTokenScenarioReturnsRequestedStatus404(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/token/404", nil)
	req.SetPathValue("scenario", "404")

	rec := httptest.NewRecorder()
	handleTokenScenario(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestTimeoutScenarioReturnsWhenContextCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/test/http/timeout", nil).WithContext(ctx)
	req.SetPathValue("scenario", "timeout")

	done := make(chan struct{})
	go func() {
		handleHTTPScenario(httptest.NewRecorder(), req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout scenario did not return after context cancellation")
	}
}

func TestHandleCatchAllOKConsumesPayloadBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"hello":"world"}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handleCatchAllOK(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	got, ok := payload["request_body_bytes"].(float64)
	if !ok {
		t.Fatalf("expected request_body_bytes in response, got %v", payload["request_body_bytes"])
	}
	if int64(got) <= 0 {
		t.Fatalf("expected request_body_bytes > 0, got %v", got)
	}
}

func TestHandleCreateUserAllowsEmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/users", nil)

	rec := httptest.NewRecorder()
	handleCreateUser(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d with body %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["request_body_bytes"] != float64(0) {
		t.Fatalf("expected request_body_bytes to be 0, got %v", payload["request_body_bytes"])
	}
}

func TestHandleCreateOrderAllowsEmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/orders", nil)

	rec := httptest.NewRecorder()
	handleCreateOrder(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d with body %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["request_body_bytes"] != float64(0) {
		t.Fatalf("expected request_body_bytes to be 0, got %v", payload["request_body_bytes"])
	}
}
