package handler

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

var allowedScriptFiles = map[string]string{
	"current-test.js": "application/javascript",
	"config.json":     "application/json",
	"k6-env.sh":       "text/x-shellscript",
}

type ScriptsHandler struct {
	scriptsDir string
	logger     *slog.Logger
}

func NewScriptsHandler(scriptsDir string, logger *slog.Logger) *ScriptsHandler {
	return &ScriptsHandler{
		scriptsDir: scriptsDir,
		logger:     logger,
	}
}

func (h *ScriptsHandler) ServeScript(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	contentType, ok := allowedScriptFiles[filename]
	if !ok {
		httpError(w, "not found", http.StatusNotFound)
		return
	}

	cleanDir := filepath.Clean(h.scriptsDir)
	path := filepath.Join(cleanDir, filename)
	cleanPath := filepath.Clean(path)

	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		h.logger.Error("resolve script path failed", "error", err, "filename", filename)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		httpError(w, "forbidden", http.StatusForbidden)
		return
	}

	file, err := os.Open(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			httpError(w, "not found", http.StatusNotFound)
			return
		}
		h.logger.Error("open script file failed", "error", err, "path", cleanPath)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := io.Copy(w, file); err != nil {
		h.logger.Error("stream script file failed", "error", err, "path", cleanPath)
	}
}
