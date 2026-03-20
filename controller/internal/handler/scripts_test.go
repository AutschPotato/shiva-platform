package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestScriptsHandlerServeScript(t *testing.T) {
	scriptsDir := t.TempDir()

	files := map[string]string{
		"current-test.js": "export default function () {};\n",
		"config.json":     "{\"vus\":1}\n",
		"k6-env.sh":       "K6_ENV_FLAGS=\"-e TARGET_URL='http://example.com'\"\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(scriptsDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	handler := NewScriptsHandler(scriptsDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := chi.NewRouter()
	router.Get("/api/internal/scripts/{filename}", handler.ServeScript)

	testCases := []struct {
		name               string
		target             string
		expectedStatus     int
		expectedBody       string
		expectedTypePrefix string
		expectedCache      string
	}{
		{
			name:               "serves javascript file",
			target:             "/api/internal/scripts/current-test.js",
			expectedStatus:     http.StatusOK,
			expectedBody:       files["current-test.js"],
			expectedTypePrefix: "application/javascript",
			expectedCache:      "no-cache",
		},
		{
			name:               "serves config file",
			target:             "/api/internal/scripts/config.json",
			expectedStatus:     http.StatusOK,
			expectedBody:       files["config.json"],
			expectedTypePrefix: "application/json",
			expectedCache:      "no-cache",
		},
		{
			name:               "serves env file",
			target:             "/api/internal/scripts/k6-env.sh",
			expectedStatus:     http.StatusOK,
			expectedBody:       files["k6-env.sh"],
			expectedTypePrefix: "text/x-shellscript",
			expectedCache:      "no-cache",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Fatalf("expected status %d, got %d", tc.expectedStatus, rec.Code)
			}
			if body := rec.Body.String(); body != tc.expectedBody {
				t.Fatalf("expected body %q, got %q", tc.expectedBody, body)
			}
			if tc.expectedTypePrefix != "" && !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.expectedTypePrefix) {
				t.Fatalf("expected content type prefix %q, got %q", tc.expectedTypePrefix, rec.Header().Get("Content-Type"))
			}
			if tc.expectedCache != "" && rec.Header().Get("Cache-Control") != tc.expectedCache {
				t.Fatalf("expected cache control %q, got %q", tc.expectedCache, rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestScriptsHandlerReturnsNotFoundForMissingWhitelistedFile(t *testing.T) {
	handler := NewScriptsHandler(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := chi.NewRouter()
	router.Get("/api/internal/scripts/{filename}", handler.ServeScript)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/scripts/current-test.js", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
	if body := rec.Body.String(); body != "{\"error\":\"not found\"}\n" {
		t.Fatalf("expected not found body, got %q", body)
	}
}

func TestScriptsHandlerRejectsNonWhitelistedFilename(t *testing.T) {
	handler := NewScriptsHandler(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/internal/scripts/%2e%2e%2fetc%2fpasswd", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("filename", "../etc/passwd")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.ServeScript(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
	if body := rec.Body.String(); body != "{\"error\":\"not found\"}\n" {
		t.Fatalf("expected not found body, got %q", body)
	}
}
