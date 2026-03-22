package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shiva-load-testing/controller/internal/handler"
)

func TestScriptsEndpointIsPublicWhileProtectedRoutesRemainGuarded(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(Deps{
		TestHandler: handler.NewTestHandler(nil, nil, logger, t.TempDir(), t.TempDir(), nil, "http://controller:8080"),
		Logger:      logger,
		JWTSecret:   "test-secret",
		CORSOrigins: []string{"http://localhost:3000"},
		ScriptsDir:  t.TempDir(),
	})

	publicReq := httptest.NewRequest(http.MethodGet, "/api/internal/scripts/current-test.js", nil)
	publicRec := httptest.NewRecorder()
	router.ServeHTTP(publicRec, publicReq)

	if publicRec.Code != http.StatusNotFound {
		t.Fatalf("expected public scripts route to bypass auth and return 404 for missing file, got %d", publicRec.Code)
	}

	uploadReq := httptest.NewRequest(http.MethodPost, "/api/internal/runs/run-1/workers/worker1/summary", nil)
	uploadRec := httptest.NewRecorder()
	router.ServeHTTP(uploadRec, uploadReq)

	if uploadRec.Code == http.StatusUnauthorized {
		t.Fatalf("expected internal artifact route to bypass JWT middleware")
	}

	protectedReq := httptest.NewRequest(http.MethodGet, "/api/templates", nil)
	protectedRec := httptest.NewRecorder()
	router.ServeHTTP(protectedRec, protectedReq)

	if protectedRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected protected route to require auth and return 401, got %d", protectedRec.Code)
	}
}
