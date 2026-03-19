package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/shiva-load-testing/controller/internal/middleware"
	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/secrets"
	"github.com/shiva-load-testing/controller/internal/store"
)

type TemplateHandler struct {
	store     *store.Store
	logger    *slog.Logger
	secretSvc *secrets.Service
}

type templateRequester struct {
	userID   int64
	username string
	role     string
}

type systemTemplateExportEnvelope struct {
	Version    int                         `json:"version"`
	ExportedAt time.Time                   `json:"exported_at"`
	Template   *model.TestTemplateRequest  `json:"template,omitempty"`
	Templates  []model.TestTemplateRequest `json:"templates,omitempty"`
}

func NewTemplateHandler(s *store.Store, logger *slog.Logger, encryptionKey string) *TemplateHandler {
	var secretSvc *secrets.Service
	if encryptionKey != "" {
		if svc, err := secrets.NewService(encryptionKey); err == nil {
			secretSvc = svc
		}
	}
	return &TemplateHandler{store: s, logger: logger, secretSvc: secretSvc}
}

func currentTemplateRequester(r *http.Request) templateRequester {
	return templateRequester{
		userID:   middleware.GetUserID(r.Context()),
		username: middleware.GetUsername(r.Context()),
		role:     middleware.GetRole(r.Context()),
	}
}

func (h *TemplateHandler) loadTemplate(r *http.Request, id string) (*model.TestTemplate, error) {
	return h.store.GetTemplate(r.Context(), id)
}

func canViewTemplate(role string, userID int64, t *model.TestTemplate) bool {
	return role == "admin" || t.System || t.UserID == userID
}

func canManageTemplate(role string, userID int64, t *model.TestTemplate) bool {
	if role == "admin" {
		return true
	}
	if t.System {
		return false
	}
	return t.UserID == userID
}

func (h *TemplateHandler) managedTemplate(w http.ResponseWriter, r *http.Request, id string) (*model.TestTemplate, bool) {
	requester := currentTemplateRequester(r)
	existing, err := h.loadTemplate(r, id)
	if err != nil {
		h.logger.Error("get template failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if existing == nil {
		httpError(w, "template not found", http.StatusNotFound)
		return nil, false
	}
	if !canManageTemplate(requester.role, requester.userID, existing) {
		httpError(w, "forbidden", http.StatusForbidden)
		return nil, false
	}

	return existing, true
}

func validateTemplateRequest(req *model.TestTemplateRequest) string {
	if req.Name == "" {
		return "name is required"
	}
	if err := normalizeTemplateRequestPayload(req); err != nil {
		return err.Error()
	}
	return ""
}

func newTemplateFromRequest(req *model.TestTemplateRequest, requester templateRequester) *model.TestTemplate {
	mode := req.Mode
	if mode == "" {
		mode = "builder"
	}

	t := &model.TestTemplate{
		ID:               uuid.New().String(),
		Name:             req.Name,
		Description:      req.Description,
		Mode:             mode,
		URL:              req.URL,
		Stages:           req.Stages,
		ScriptContent:    req.ScriptContent,
		ConfigContent:    req.ConfigContent,
		HTTPMethod:       req.HTTPMethod,
		ContentType:      req.ContentType,
		PayloadJSON:      req.PayloadJSON,
		PayloadTargetKiB: req.PayloadTargetKiB,
		AuthConfig:       authConfigFromStoredInput(req.Auth, nil),
		UserID:           requester.userID,
		Username:         requester.username,
	}
	return t
}

func (h *TemplateHandler) exportableTemplateRequest(t *model.TestTemplate) (*model.TestTemplateRequest, error) {
	authInput := model.AuthInput{
		Enabled:            t.AuthConfig.Enabled,
		Mode:               t.AuthConfig.Mode,
		TokenURL:           t.AuthConfig.TokenURL,
		ClientID:           t.AuthConfig.ClientID,
		ClientAuthMethod:   t.AuthConfig.ClientAuthMethod,
		RefreshSkewSeconds: t.AuthConfig.RefreshSkewSeconds,
		PersistSecret:      t.AuthConfig.SecretConfigured,
	}

	if t.AuthConfig.SecretConfigured && t.AuthConfig.ClientSecretEncrypted != "" {
		if h.secretSvc == nil {
			return nil, httpStatusError{Message: "template auth secret export is unavailable because encryption is not configured", Code: http.StatusConflict}
		}
		secret, err := h.secretSvc.Decrypt(t.AuthConfig.ClientSecretEncrypted)
		if err != nil {
			return nil, httpStatusError{Message: "failed to decrypt template auth secret for export", Code: http.StatusInternalServerError}
		}
		authInput.ClientSecret = secret
	}

	return &model.TestTemplateRequest{
		Name:             t.Name,
		Description:      t.Description,
		Mode:             t.Mode,
		URL:              t.URL,
		Stages:           t.Stages,
		ScriptContent:    t.ScriptContent,
		ConfigContent:    t.ConfigContent,
		HTTPMethod:       t.HTTPMethod,
		ContentType:      t.ContentType,
		PayloadJSON:      t.PayloadJSON,
		PayloadTargetKiB: t.PayloadTargetKiB,
		Auth:             authInput,
	}, nil
}

type httpStatusError struct {
	Message string
	Code    int
}

func (e httpStatusError) Error() string { return e.Message }

func parseSystemTemplateImportPayload(body []byte) ([]model.TestTemplateRequest, error) {
	var envelope systemTemplateExportEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil {
		if envelope.Template != nil {
			return []model.TestTemplateRequest{*envelope.Template}, nil
		}
		if len(envelope.Templates) > 0 {
			return envelope.Templates, nil
		}
	}

	var single model.TestTemplateRequest
	if err := json.Unmarshal(body, &single); err == nil && strings.TrimSpace(single.Name) != "" {
		return []model.TestTemplateRequest{single}, nil
	}

	var many []model.TestTemplateRequest
	if err := json.Unmarshal(body, &many); err == nil && len(many) > 0 {
		return many, nil
	}

	return nil, httpStatusError{Message: "invalid system template import payload", Code: http.StatusBadRequest}
}

func (h *TemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	requester := currentTemplateRequester(r)

	templates, err := h.store.ListTemplates(r.Context(), requester.userID, requester.role)
	if err != nil {
		h.logger.Error("list templates failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if templates == nil {
		templates = []model.TestTemplate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"templates": templates,
		"total":     len(templates),
	})
}

func (h *TemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	t, err := h.loadTemplate(r, id)
	if err != nil {
		h.logger.Error("get template failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if t == nil {
		httpError(w, "template not found", http.StatusNotFound)
		return
	}

	requester := currentTemplateRequester(r)
	if !canViewTemplate(requester.role, requester.userID, t) {
		httpError(w, "forbidden", http.StatusForbidden)
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	requester := currentTemplateRequester(r)

	var req model.TestTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if msg := validateTemplateRequest(&req); msg != "" {
		httpError(w, msg, http.StatusBadRequest)
		return
	}

	t := newTemplateFromRequest(&req, requester)
	authCfg, err := buildStoredAuthConfig(req.Auth, nil, h.secretSvc, false)
	if err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.AuthConfig = authCfg

	if err := h.store.CreateTemplate(r.Context(), t); err != nil {
		h.logger.Error("create template failed", "error", err)
		httpError(w, "failed to create template", http.StatusInternalServerError)
		return
	}

	h.logger.Info("template created", "id", t.ID, "name", t.Name, "user", requester.username)
	writeJSON(w, http.StatusCreated, t)
}

func (h *TemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, ok := h.managedTemplate(w, r, id)
	if !ok {
		return
	}

	var req model.TestTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if msg := validateTemplateRequest(&req); msg != "" {
		httpError(w, msg, http.StatusBadRequest)
		return
	}

	existing.Name = req.Name
	existing.Description = req.Description
	existing.URL = req.URL
	existing.Stages = req.Stages
	existing.ScriptContent = req.ScriptContent
	existing.ConfigContent = req.ConfigContent
	existing.HTTPMethod = req.HTTPMethod
	existing.ContentType = req.ContentType
	existing.PayloadJSON = req.PayloadJSON
	existing.PayloadTargetKiB = req.PayloadTargetKiB
	authCfg, err := buildStoredAuthConfig(req.Auth, &existing.AuthConfig, h.secretSvc, false)
	if err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}
	existing.AuthConfig = authCfg

	if err := h.store.UpdateTemplate(r.Context(), existing); err != nil {
		h.logger.Error("update template failed", "error", err)
		httpError(w, "failed to update template", http.StatusInternalServerError)
		return
	}

	h.logger.Info("template updated", "id", id)
	writeJSON(w, http.StatusOK, existing)
}

func (h *TemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, ok := h.managedTemplate(w, r, id)
	if !ok {
		return
	}

	if err := h.store.DeleteTemplate(r.Context(), id); err != nil {
		h.logger.Error("delete template failed", "error", err)
		httpError(w, "failed to delete template", http.StatusInternalServerError)
		return
	}

	h.logger.Info("template deleted", "id", id, "name", existing.Name, "system", existing.System)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *TemplateHandler) PromoteToSystem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.loadTemplate(r, id)
	if err != nil {
		h.logger.Error("get template failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpError(w, "template not found", http.StatusNotFound)
		return
	}

	if err := h.store.SetTemplateSystem(r.Context(), id, true); err != nil {
		h.logger.Error("promote template failed", "error", err)
		httpError(w, "failed to promote template", http.StatusInternalServerError)
		return
	}

	existing.System = true
	h.logger.Info("template promoted to system", "id", id, "name", existing.Name)
	writeJSON(w, http.StatusOK, existing)
}

func (h *TemplateHandler) DemoteFromSystem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.loadTemplate(r, id)
	if err != nil {
		h.logger.Error("get template failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpError(w, "template not found", http.StatusNotFound)
		return
	}

	if err := h.store.SetTemplateSystem(r.Context(), id, false); err != nil {
		h.logger.Error("demote template failed", "error", err)
		httpError(w, "failed to demote template", http.StatusInternalServerError)
		return
	}

	existing.System = false
	h.logger.Info("template demoted from system", "id", id, "name", existing.Name)
	writeJSON(w, http.StatusOK, existing)
}

func (h *TemplateHandler) Export(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.loadTemplate(r, id)
	if err != nil {
		h.logger.Error("get template failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpError(w, "template not found", http.StatusNotFound)
		return
	}
	if !existing.System {
		httpError(w, "only system templates can be exported here", http.StatusBadRequest)
		return
	}

	exportReq, err := h.exportableTemplateRequest(existing)
	if err != nil {
		if httpErr, ok := err.(httpStatusError); ok {
			httpError(w, httpErr.Message, httpErr.Code)
			return
		}
		h.logger.Error("export template failed", "error", err, "id", id)
		httpError(w, "failed to export template", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, systemTemplateExportEnvelope{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Template:   exportReq,
	})
}

func (h *TemplateHandler) ExportSystemTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.store.ListSystemTemplates(r.Context())
	if err != nil {
		h.logger.Error("list system templates failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	exported := make([]model.TestTemplateRequest, 0, len(templates))
	for _, t := range templates {
		exportReq, err := h.exportableTemplateRequest(&t)
		if err != nil {
			if httpErr, ok := err.(httpStatusError); ok {
				httpError(w, httpErr.Message, httpErr.Code)
				return
			}
			h.logger.Error("export system templates failed", "error", err, "id", t.ID)
			httpError(w, "failed to export system templates", http.StatusInternalServerError)
			return
		}
		exported = append(exported, *exportReq)
	}

	writeJSON(w, http.StatusOK, systemTemplateExportEnvelope{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Templates:  exported,
	})
}

func (h *TemplateHandler) ImportSystemTemplates(w http.ResponseWriter, r *http.Request) {
	requester := currentTemplateRequester(r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	reqs, err := parseSystemTemplateImportPayload(body)
	if err != nil {
		if httpErr, ok := err.(httpStatusError); ok {
			httpError(w, httpErr.Message, httpErr.Code)
			return
		}
		httpError(w, "invalid system template import payload", http.StatusBadRequest)
		return
	}

	imported := make([]model.TestTemplate, 0, len(reqs))
	for _, req := range reqs {
		if msg := validateTemplateRequest(&req); msg != "" {
			httpError(w, msg, http.StatusBadRequest)
			return
		}
		if req.Auth.Enabled && strings.TrimSpace(req.Auth.ClientSecret) != "" {
			req.Auth.PersistSecret = true
		}

		t := newTemplateFromRequest(&req, requester)
		t.System = true
		authCfg, err := buildStoredAuthConfig(req.Auth, nil, h.secretSvc, false)
		if err != nil {
			httpError(w, err.Error(), http.StatusBadRequest)
			return
		}
		t.AuthConfig = authCfg

		if err := h.store.CreateTemplate(r.Context(), t); err != nil {
			h.logger.Error("import system template failed", "error", err, "name", t.Name)
			httpError(w, "failed to import system template", http.StatusInternalServerError)
			return
		}
		imported = append(imported, *t)
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"templates": imported,
		"total":     len(imported),
	})
}
