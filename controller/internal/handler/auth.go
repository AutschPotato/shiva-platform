package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/shiva-load-testing/controller/internal/middleware"
	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/store"
)

type AuthHandler struct {
	store                 *store.Store
	jwtSecret             string
	logger                *slog.Logger
	publicAppURL          string
	passwordResetTokenTTL time.Duration
	smtpHost              string
	smtpPort              int
	smtpUser              string
	smtpPassword          string
	smtpFromEmail         string
	smtpFromName          string
}

const tempPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

type AuthHandlerOptions struct {
	PublicAppURL          string
	PasswordResetTokenTTL time.Duration
	SMTPHost              string
	SMTPPort              int
	SMTPUser              string
	SMTPPassword          string
	SMTPFromEmail         string
	SMTPFromName          string
}

func NewAuthHandler(s *store.Store, jwtSecret string, logger *slog.Logger, opts AuthHandlerOptions) *AuthHandler {
	return &AuthHandler{
		store:                 s,
		jwtSecret:             jwtSecret,
		logger:                logger,
		publicAppURL:          strings.TrimRight(opts.PublicAppURL, "/"),
		passwordResetTokenTTL: opts.PasswordResetTokenTTL,
		smtpHost:              opts.SMTPHost,
		smtpPort:              opts.SMTPPort,
		smtpUser:              opts.SMTPUser,
		smtpPassword:          opts.SMTPPassword,
		smtpFromEmail:         opts.SMTPFromEmail,
		smtpFromName:          opts.SMTPFromName,
	}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := h.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		h.logger.Error("login db error", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		httpError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.HashedPassword), []byte(req.Password)); err != nil {
		httpError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := h.generateToken(user)
	if err != nil {
		h.logger.Error("token generation failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := model.LoginResponse{Token: token, User: *user}
	writeJSON(w, http.StatusOK, resp)
}

func (h *AuthHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		h.logger.Error("list users failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *AuthHandler) GetProfileSummary(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		h.logger.Error("load profile user failed", "error", err, "user_id", userID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		httpError(w, "user not found", http.StatusNotFound)
		return
	}

	metrics, err := h.store.GetUserMetricsByID(r.Context(), userID)
	if err != nil {
		h.logger.Error("load profile metrics failed", "error", err, "user_id", userID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	user.HashedPassword = ""
	writeJSON(w, http.StatusOK, model.ProfileSummaryResponse{
		User:    *user,
		Metrics: metrics,
	})
}

func (h *AuthHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req model.CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		httpError(w, "username, email and password are required", http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	user := &model.User{
		Username:       req.Username,
		Email:          req.Email,
		HashedPassword: string(hashed),
		Role:           req.Role,
	}
	if err := h.store.CreateUser(r.Context(), user); err != nil {
		h.logger.Error("create user failed", "error", err)
		httpError(w, fmt.Sprintf("could not create user: %v", err), http.StatusConflict)
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

func (h *AuthHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	var req model.UpdatePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		httpError(w, "user not found", http.StatusNotFound)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.HashedPassword), []byte(req.OldPassword)); err != nil {
		httpError(w, "current password is incorrect", http.StatusUnauthorized)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.store.UpdatePassword(r.Context(), userID, string(hashed)); err != nil {
		h.logger.Error("update password failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	user.HashedPassword = ""
	user.MustChangePassword = false
	writeJSON(w, http.StatusOK, model.UpdatePasswordResponse{
		Message: "password updated",
		User:    *user,
	})
}

func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req model.ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resp := model.ForgotPasswordResponse{
		Message: "If an account matches that identifier, a password reset link has been sent.",
	}

	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	user, err := h.store.GetUserByIdentifier(r.Context(), identifier)
	if err != nil {
		h.logger.Error("forgot password lookup failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	rawToken, tokenHash, err := generateResetToken()
	if err != nil {
		h.logger.Error("generate password reset token failed", "error", err, "user_id", user.ID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().UTC().Add(h.effectivePasswordResetTTL())
	if err := h.store.CreatePasswordResetToken(r.Context(), &model.PasswordResetToken{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}); err != nil {
		h.logger.Error("store password reset token failed", "error", err, "user_id", user.ID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	resetURL := h.buildPasswordResetURL(rawToken)
	if err := h.deliverPasswordReset(user, resetURL, expiresAt); err != nil {
		h.logger.Error("deliver password reset failed", "error", err, "user_id", user.ID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *AuthHandler) CompletePasswordReset(w http.ResponseWriter, r *http.Request) {
	var req model.CompletePasswordResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Token) == "" || strings.TrimSpace(req.NewPassword) == "" {
		httpError(w, "token and new password are required", http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, err := h.store.ResetPasswordWithToken(r.Context(), hashResetToken(req.Token), string(hashedPassword))
	if err != nil {
		h.logger.Error("reset password with token failed", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		httpError(w, "invalid or expired reset token", http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, model.CompletePasswordResetResponse{
		Message: "password reset successful",
	})
}

func (h *AuthHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || userID <= 0 {
		httpError(w, "invalid user id", http.StatusBadRequest)
		return
	}

	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		h.logger.Error("load user for reset failed", "error", err, "user_id", userID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		httpError(w, "user not found", http.StatusNotFound)
		return
	}

	tempPassword, err := generateTemporaryPassword(14)
	if err != nil {
		h.logger.Error("generate temp password failed", "error", err, "user_id", userID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.store.AdminResetPassword(r.Context(), userID, string(hashed)); err != nil {
		h.logger.Error("admin reset password failed", "error", err, "user_id", userID)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	user.HashedPassword = ""
	user.MustChangePassword = true

	writeJSON(w, http.StatusOK, model.AdminResetPasswordResponse{
		Message:           "password reset",
		TemporaryPassword: tempPassword,
		User:              *user,
	})
}

func (h *AuthHandler) generateToken(user *model.User) (string, error) {
	claims := jwt.MapClaims{
		"sub":      strconv.FormatInt(user.ID, 10),
		"username": user.Username,
		"role":     user.Role,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.jwtSecret))
}

// HashPassword creates a bcrypt hash (exported for initial admin setup).
func HashPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hashed), err
}

func httpError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func generateTemporaryPassword(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("invalid temp password length")
	}
	buf := make([]byte, length)
	max := big.NewInt(int64(len(tempPasswordAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = tempPasswordAlphabet[n.Int64()]
	}
	return string(buf), nil
}

func generateResetToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw)
	return token, hashResetToken(token), nil
}

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (h *AuthHandler) effectivePasswordResetTTL() time.Duration {
	if h.passwordResetTokenTTL <= 0 {
		return 30 * time.Minute
	}
	return h.passwordResetTokenTTL
}

func (h *AuthHandler) buildPasswordResetURL(token string) string {
	baseURL := h.publicAppURL
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	return fmt.Sprintf("%s/reset-password?token=%s", strings.TrimRight(baseURL, "/"), token)
}

func (h *AuthHandler) deliverPasswordReset(user *model.User, resetURL string, expiresAt time.Time) error {
	if strings.TrimSpace(h.smtpHost) == "" {
		h.logger.Warn("smtp not configured; password reset link emitted to logs",
			"user_id", user.ID,
			"username", user.Username,
			"email", user.Email,
			"reset_url", resetURL,
			"expires_at", expiresAt.Format(time.RFC3339),
		)
		return nil
	}
	return h.sendPasswordResetEmail(user, resetURL, expiresAt)
}

func (h *AuthHandler) sendPasswordResetEmail(user *model.User, resetURL string, expiresAt time.Time) error {
	fromAddress := strings.TrimSpace(h.smtpFromEmail)
	if fromAddress == "" {
		return fmt.Errorf("smtp from email is required")
	}

	headers := []string{
		fmt.Sprintf("From: %s", formatEmailAddress(h.smtpFromName, fromAddress)),
		fmt.Sprintf("To: %s", user.Email),
		"Subject: Password reset request",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	body := fmt.Sprintf(
		"Hello %s,\r\n\r\nUse the following link to reset your password:\r\n%s\r\n\r\nThe link expires at %s.\r\nIf you did not request this change, you can ignore this email.\r\n",
		user.Username,
		resetURL,
		expiresAt.Format(time.RFC1123),
	)
	message := strings.Join(headers, "\r\n") + "\r\n\r\n" + body

	address := fmt.Sprintf("%s:%d", h.smtpHost, h.smtpPort)
	var auth smtp.Auth
	if strings.TrimSpace(h.smtpUser) != "" {
		auth = smtp.PlainAuth("", h.smtpUser, h.smtpPassword, h.smtpHost)
	}
	return smtp.SendMail(address, auth, fromAddress, []string{user.Email}, []byte(message))
}

func formatEmailAddress(name, email string) string {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return email
	}
	return fmt.Sprintf("%s <%s>", trimmedName, email)
}
