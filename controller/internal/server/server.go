package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/shiva-load-testing/controller/internal/handler"
	"github.com/shiva-load-testing/controller/internal/middleware"
	"github.com/shiva-load-testing/controller/internal/orchestrator"
	"github.com/shiva-load-testing/controller/internal/scheduler"
	"github.com/shiva-load-testing/controller/internal/store"
)

type Deps struct {
	Store                 *store.Store
	Orchestrator          *orchestrator.Orchestrator
	Scheduler             *scheduler.Scheduler
	TestHandler           *handler.TestHandler
	Logger                *slog.Logger
	JWTSecret             string
	APIKey                string
	CORSOrigins           []string
	ScriptsDir            string
	OutputDir             string
	PublicAppURL          string
	PasswordResetTokenTTL time.Duration
	SMTPHost              string
	SMTPPort              int
	SMTPUser              string
	SMTPPassword          string
	SMTPFromEmail         string
	SMTPFromName          string
	EncryptionKey         string
}

func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(middleware.CORS(deps.CORSOrigins))

	// Handlers
	authH := handler.NewAuthHandler(deps.Store, deps.JWTSecret, deps.Logger, handler.AuthHandlerOptions{
		PublicAppURL:          deps.PublicAppURL,
		PasswordResetTokenTTL: deps.PasswordResetTokenTTL,
		SMTPHost:              deps.SMTPHost,
		SMTPPort:              deps.SMTPPort,
		SMTPUser:              deps.SMTPUser,
		SMTPPassword:          deps.SMTPPassword,
		SMTPFromEmail:         deps.SMTPFromEmail,
		SMTPFromName:          deps.SMTPFromName,
	})
	testH := deps.TestHandler
	resultH := handler.NewResultHandler(deps.Store, deps.Logger)
	dashboardH := handler.NewDashboardHandler(deps.Orchestrator, deps.Logger)
	healthH := handler.NewHealthHandler(deps.Orchestrator)
	scriptsH := handler.NewScriptsHandler(deps.ScriptsDir, deps.Logger)
	templateH := handler.NewTemplateHandler(deps.Store, deps.Logger, deps.EncryptionKey)
	scheduleH := handler.NewScheduleHandler(deps.Store, deps.Scheduler, deps.Orchestrator, deps.Logger, deps.EncryptionKey)

	// Public routes
	r.Get("/api/health", healthH.Health)
	r.Post("/api/auth/login", authH.Login)
	r.Post("/api/auth/forgot-password", authH.ForgotPassword)
	r.Post("/api/auth/reset-password", authH.CompletePasswordReset)
	r.Get("/api/internal/scripts/{filename}", scriptsH.ServeScript)
	r.Post("/api/internal/runs/{testID}/workers/{workerID}/{artifactType}", testH.UploadArtifact)

	// Protected routes (JWT + optional API key)
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(deps.JWTSecret))
		if deps.APIKey != "" {
			r.Use(middleware.APIKeyAuth(deps.APIKey))
		}

		// Test execution
		r.Post("/api/run", testH.StartTest)
		r.Post("/api/stop", testH.StopTest)
		r.Post("/api/pause", testH.PauseTest)
		r.Post("/api/resume", testH.ResumeTest)
		r.Post("/api/scale", testH.ScaleTest)
		r.Get("/api/metrics/live", testH.GetLiveMetrics)
		r.Get("/api/workers/status", testH.GetWorkerStatus)

		// Scripts
		r.Post("/api/scripts/upload", testH.UploadScript)

		// Results
		r.Get("/api/result/list", resultH.ListResults)
		r.Get("/api/result/{id}", resultH.GetResult)

		// Templates
		r.Get("/api/templates", templateH.List)
		r.Post("/api/templates", templateH.Create)
		r.Get("/api/templates/{id}", templateH.Get)
		r.Put("/api/templates/{id}", templateH.Update)
		r.Delete("/api/templates/{id}", templateH.Delete)

		// Schedules
		r.Post("/api/schedules", scheduleH.Create)
		r.Get("/api/schedules", scheduleH.List)
		r.Get("/api/schedules/calendar", scheduleH.Calendar)
		r.Post("/api/schedules/check-conflict", scheduleH.CheckConflict)
		r.Get("/api/schedules/{id}", scheduleH.Get)
		r.Put("/api/schedules/{id}", scheduleH.Update)
		r.Delete("/api/schedules/{id}", scheduleH.Delete)
		r.Post("/api/schedules/{id}/pause", scheduleH.Pause)
		r.Post("/api/schedules/{id}/resume", scheduleH.Resume)
		r.Post("/api/schedules/{id}/run-now", scheduleH.RunNow)
		r.Get("/api/schedules/{id}/executions", scheduleH.ListExecutions)

		// Profile
		r.Get("/api/profile", authH.GetProfileSummary)
		r.Put("/api/profile/password", authH.UpdatePassword)

		// Admin routes
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdmin)
			r.Get("/api/auth/users", authH.ListUsers)
			r.Post("/api/auth/users", authH.CreateUser)
			r.Post("/api/auth/users/{id}/reset-password", authH.ResetUserPassword)
			r.Post("/api/resetdata", resultH.ResetData)
			r.Get("/api/admin/templates/system/export", templateH.ExportSystemTemplates)
			r.Post("/api/admin/templates/system/import", templateH.ImportSystemTemplates)
			r.Post("/api/admin/templates/{id}/system", templateH.PromoteToSystem)
			r.Delete("/api/admin/templates/{id}/system", templateH.DemoteFromSystem)
			r.Get("/api/admin/templates/{id}/export", templateH.Export)
			r.Get("/api/admin/workers/dashboards", dashboardH.ListDashboards)
			r.Get("/api/admin/workers/{worker}/dashboard", dashboardH.ProxyDashboard)
			r.Get("/api/admin/workers/{worker}/dashboard/{proxyPath}", dashboardH.ProxyDashboard)
			r.Get("/api/admin/workers/{worker}/dashboard/*", dashboardH.ProxyDashboard)
		})
	})

	return r
}
