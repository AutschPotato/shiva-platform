package model

import (
	"encoding/json"
	"time"
)

// --- Identity and authentication ---

type User struct {
	ID                 int64     `json:"id"`
	Username           string    `json:"username"`
	Email              string    `json:"email"`
	HashedPassword     string    `json:"-"`
	Role               string    `json:"role"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type CreateUserRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type UpdatePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type UpdatePasswordResponse struct {
	Message string `json:"message"`
	User    User   `json:"user"`
}

type ForgotPasswordRequest struct {
	Identifier string `json:"identifier"`
}

type ForgotPasswordResponse struct {
	Message string `json:"message"`
}

type CompletePasswordResetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type CompletePasswordResetResponse struct {
	Message string `json:"message"`
}

type AdminResetPasswordResponse struct {
	Message           string `json:"message"`
	TemporaryPassword string `json:"temporary_password"`
	User              User   `json:"user"`
}

type PasswordResetToken struct {
	ID        int64
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

type AdminUserMetrics struct {
	TotalTests      int        `json:"total_tests"`
	CompletedTests  int        `json:"completed_tests"`
	FailedTests     int        `json:"failed_tests"`
	TotalSchedules  int        `json:"total_schedules"`
	ActiveSchedules int        `json:"active_schedules"`
	TotalTemplates  int        `json:"total_templates"`
	LastTestAt      *time.Time `json:"last_test_at,omitempty"`
}

type AdminUserRow struct {
	ID                 int64            `json:"id"`
	Username           string           `json:"username"`
	Email              string           `json:"email"`
	Role               string           `json:"role"`
	MustChangePassword bool             `json:"must_change_password"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	Metrics            AdminUserMetrics `json:"metrics"`
}

type ProfileSummaryResponse struct {
	User    User             `json:"user"`
	Metrics AdminUserMetrics `json:"metrics"`
}

// --- Authentication configuration shared across builder, templates and schedules ---

type AuthConfig struct {
	Enabled               bool   `json:"auth_enabled"`
	Mode                  string `json:"auth_mode,omitempty"`
	TokenURL              string `json:"auth_token_url,omitempty"`
	ClientID              string `json:"auth_client_id,omitempty"`
	ClientAuthMethod      string `json:"auth_client_auth_method,omitempty"`
	RefreshSkewSeconds    int    `json:"auth_refresh_skew_seconds,omitempty"`
	SecretSource          string `json:"auth_secret_source,omitempty"`
	SecretConfigured      bool   `json:"auth_secret_configured,omitempty"`
	ClientSecretEncrypted string `json:"-"`
}

type AuthInput struct {
	Enabled            bool   `json:"auth_enabled"`
	Mode               string `json:"auth_mode,omitempty"`
	TokenURL           string `json:"auth_token_url,omitempty"`
	ClientID           string `json:"auth_client_id,omitempty"`
	ClientSecret       string `json:"auth_client_secret,omitempty"`
	ClientAuthMethod   string `json:"auth_client_auth_method,omitempty"`
	RefreshSkewSeconds int    `json:"auth_refresh_skew_seconds,omitempty"`
	PersistSecret      bool   `json:"auth_persist_secret,omitempty"`
	ClearSecret        bool   `json:"auth_clear_secret,omitempty"`
}

// --- Load test definition and execution input ---

type LoadTest struct {
	ID                string          `json:"id"`
	ProjectName       string          `json:"project_name"`
	URL               string          `json:"url"`
	Status            string          `json:"status"`
	ResultJSON        json.RawMessage `json:"result_json,omitempty"`
	ScriptContent     string          `json:"script_content,omitempty"`
	ConfigContent     string          `json:"config_content,omitempty"`
	PayloadSourceJSON string          `json:"payload_source_json,omitempty"`
	PayloadContent    string          `json:"payload_content,omitempty"`
	HTTPMethod        string          `json:"http_method,omitempty"`
	ContentType       string          `json:"content_type,omitempty"`
	AuthConfig        AuthConfig      `json:"auth,omitempty"`
	UserID            int64           `json:"user_id"`
	Username          string          `json:"username"`
	CreatedAt         time.Time       `json:"created_at"`
}

type TestRequest struct {
	ProjectName      string    `json:"project_name"`
	URL              string    `json:"url"`
	Executor         string    `json:"executor,omitempty"` // "ramping-vus", "constant-vus", "constant-arrival-rate", "ramping-arrival-rate"
	Stages           []Stage   `json:"stages,omitempty"`
	VUs              int       `json:"vus,omitempty"`               // constant-vus
	Duration         string    `json:"duration,omitempty"`          // constant-vus, constant-arrival-rate
	Rate             int       `json:"rate,omitempty"`              // constant-arrival-rate
	TimeUnit         string    `json:"time_unit,omitempty"`         // arrival-rate: "1s" or "1m"
	PreAllocatedVUs  int       `json:"pre_allocated_vus,omitempty"` // arrival-rate
	MaxVUs           int       `json:"max_vus,omitempty"`           // arrival-rate
	SleepSeconds     *float64  `json:"sleep_seconds,omitempty"`     // think-time between iterations (default 0.5 for VU executors, 0 for arrival-rate)
	ScriptContent    string    `json:"script_content,omitempty"`
	ConfigContent    string    `json:"config_content,omitempty"`
	HTTPMethod       string    `json:"http_method,omitempty"`
	ContentType      string    `json:"content_type,omitempty"`
	PayloadJSON      string    `json:"payload_json,omitempty"`
	PayloadTargetKiB int       `json:"payload_target_kib,omitempty"`
	Auth             AuthInput `json:"auth,omitempty"`
}

// Stage is shared by builder-mode tests, templates and schedules.
type Stage struct {
	Duration string `json:"duration"`
	Target   int    `json:"target"`
}

// ConflictWarning represents a detected conflict between script and config.
type ConflictWarning struct {
	Type    string `json:"type"` // "redundancy", "entry_point", "metric_consistency", "schema"
	Message string `json:"message"`
}

// --- k6 runtime API contracts ---

// K6 REST API types

// K6 status codes: 0=created, 1=init, 2=initialized, 3=setup, 4=running, 5=tearing_down, 6=teared_down, 7=finished
type K6Status struct {
	Status  json.RawMessage `json:"status"` // Can be int or string depending on k6 version
	Paused  bool            `json:"paused"`
	VUs     int             `json:"vus"`
	VUsMax  int             `json:"vus-max"`
	Stopped bool            `json:"stopped"`
	Running bool            `json:"running"`
	Tainted bool            `json:"tainted"`
}

// IsRunning returns true if the k6 instance is actively running a test.
// Note: the k6 REST API field "running" means "process alive", NOT "test executing".
// We use the numeric/string status code instead, which accurately reflects the test phase.
func (s *K6Status) IsRunning() bool {
	// Prefer the status code — it's the most reliable indicator
	var statusNum int
	if json.Unmarshal(s.Status, &statusNum) == nil {
		// 3=setup, 4=running, 5=tearing_down
		return statusNum >= 3 && statusNum <= 5
	}
	var statusStr string
	if json.Unmarshal(s.Status, &statusStr) == nil {
		return statusStr == "running" || statusStr == "setup" || statusStr == "tearing_down"
	}
	// Fallback: use Running field but exclude paused/stopped (process alive != test active)
	return s.Running && !s.Paused && !s.Stopped
}

// IsFinished returns true if k6 has completed.
func (s *K6Status) IsFinished() bool {
	var statusNum int
	if json.Unmarshal(s.Status, &statusNum) == nil {
		return statusNum >= 6
	}
	var statusStr string
	if json.Unmarshal(s.Status, &statusStr) == nil {
		return statusStr == "finished" || statusStr == "teared_down"
	}
	return false
}

type K6StatusPatch struct {
	Paused  *bool `json:"paused,omitempty"`
	Stopped *bool `json:"stopped,omitempty"`
	VUs     *int  `json:"vus,omitempty"`
	VUsMax  *int  `json:"vus-max,omitempty"`
}

type K6Metric struct {
	Type     string             `json:"type"`
	Contains string             `json:"contains"`
	Tainted  *bool              `json:"tainted"` // nil = no threshold, true = threshold breached
	Sample   map[string]float64 `json:"sample"`
	Values   map[string]float64 `json:"values"`
}

// Val looks up a metric value in Sample (k6 native) or Values (fallback).
func (m K6Metric) Val(key string) (float64, bool) {
	if v, ok := m.Sample[key]; ok {
		return v, true
	}
	if v, ok := m.Values[key]; ok {
		return v, true
	}
	return 0, false
}

type K6MetricsResponse struct {
	Metrics map[string]K6Metric `json:"metrics"`
}

// --- Aggregated runtime metrics and stored test output ---

type ResultListResponse struct {
	Results []LoadTest `json:"results"`
	Total   int        `json:"total"`
}

type AggregatedMetrics struct {
	TotalVUs         int               `json:"total_vus"`
	TotalRequests    float64           `json:"total_requests"`
	BusinessRequests float64           `json:"business_requests,omitempty"`
	AvgLatency       float64           `json:"avg_latency_ms"`
	MedLatency       float64           `json:"med_latency_ms"`
	P90Latency       float64           `json:"p90_latency_ms"`
	P95Latency       float64           `json:"p95_latency_ms"`
	P99Latency       float64           `json:"p99_latency_ms"`
	MinLatency       float64           `json:"min_latency_ms"`
	MaxLatency       float64           `json:"max_latency_ms"`
	ErrorRate        float64           `json:"error_rate"`
	SuccessRate      float64           `json:"success_rate"`
	RPS              float64           `json:"rps"`
	Iterations       float64           `json:"iterations"`
	DataReceived     float64           `json:"data_received_bytes"`
	DataSent         float64           `json:"data_sent_bytes"`
	HTTPFailures     float64           `json:"http_failures"`
	HTTPSuccesses    float64           `json:"http_successes"`
	Status4xx        float64           `json:"status_4xx"`
	Status5xx        float64           `json:"status_5xx"`
	Workers          []WorkerMetrics   `json:"workers"`
	Thresholds       []ThresholdResult `json:"thresholds,omitempty"`
}

type MetricQualityFlag struct {
	Key                 string `json:"key"`
	Status              string `json:"status"`
	Source              string `json:"source,omitempty"`
	Scope               string `json:"scope,omitempty"`
	ApproximationReason string `json:"approximation_reason,omitempty"`
}

type MetricCounter struct {
	Count float64 `json:"count"`
	Rate  float64 `json:"rate,omitempty"`
}

type CheckMetrics struct {
	Passes   float64 `json:"passes"`
	Fails    float64 `json:"fails"`
	PassRate float64 `json:"pass_rate"`
	FailRate float64 `json:"fail_rate"`
}

type StatusCodeCount struct {
	Code  int     `json:"code"`
	Count float64 `json:"count"`
}

type HTTPMetricsBlock struct {
	Requests          float64 `json:"requests"`
	RPS               float64 `json:"rps,omitempty"`
	Successes         float64 `json:"successes"`
	Failures          float64 `json:"failures"`
	SuccessRate       float64 `json:"success_rate"`
	ErrorRate         float64 `json:"error_rate"`
	Status2xx         float64 `json:"status_2xx,omitempty"`
	Status4xx         float64 `json:"status_4xx,omitempty"`
	Status5xx         float64 `json:"status_5xx,omitempty"`
	OtherFailures     float64 `json:"other_failures,omitempty"`
	NetworkErrors     float64 `json:"network_errors,omitempty"`
	DataReceivedBytes float64 `json:"data_received_bytes,omitempty"`
	DataSentBytes     float64 `json:"data_sent_bytes,omitempty"`
}

type LatencyMetricBlock struct {
	Metric string  `json:"metric"`
	Scope  string  `json:"scope"`
	AvgMs  float64 `json:"avg_ms"`
	MedMs  float64 `json:"med_ms"`
	P90Ms  float64 `json:"p90_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MinMs  float64 `json:"min_ms"`
	MaxMs  float64 `json:"max_ms"`
}

type BreakdownMetricBlock struct {
	AvgMs float64 `json:"avg_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

type LatencyBreakdownBlock struct {
	Blocked        BreakdownMetricBlock `json:"blocked"`
	Waiting        BreakdownMetricBlock `json:"waiting"`
	Sending        BreakdownMetricBlock `json:"sending"`
	Receiving      BreakdownMetricBlock `json:"receiving"`
	Connecting     BreakdownMetricBlock `json:"connecting"`
	TLSHandshaking BreakdownMetricBlock `json:"tls_handshaking"`
}

type WorkerMetricsV2 struct {
	Address           string  `json:"address"`
	Status            string  `json:"status"`
	Error             string  `json:"error,omitempty"`
	Requests          float64 `json:"requests"`
	BusinessRequests  float64 `json:"business_requests"`
	AuxiliaryRequests float64 `json:"auxiliary_requests"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	P95LatencyMs      float64 `json:"p95_latency_ms"`
	P99LatencyMs      float64 `json:"p99_latency_ms"`
	ErrorRate         float64 `json:"error_rate"`
	ActiveDurationS   float64 `json:"active_duration_s,omitempty"`
}

type MetricsV2 struct {
	HTTPTotal        HTTPMetricsBlock       `json:"http_total"`
	HTTPBusiness     HTTPMetricsBlock       `json:"http_business"`
	HTTPAuxiliary    HTTPMetricsBlock       `json:"http_auxiliary"`
	Iterations       MetricCounter          `json:"iterations"`
	Checks           CheckMetrics           `json:"checks"`
	PrimaryLatency   LatencyMetricBlock     `json:"latency_primary"`
	LatencyBreakdown *LatencyBreakdownBlock `json:"latency_breakdown,omitempty"`
	Workers          []WorkerMetricsV2      `json:"workers,omitempty"`
	Thresholds       []ThresholdResult      `json:"thresholds,omitempty"`
	QualityFlags     []MetricQualityFlag    `json:"quality_flags,omitempty"`
}

type ThresholdResult struct {
	Metric string `json:"metric"`
	Passed bool   `json:"passed"`
}

type WorkerMetrics struct {
	Name             string  `json:"name,omitempty"`
	Address          string  `json:"address"`
	VUs              int     `json:"vus"`
	Requests         float64 `json:"requests"`
	AvgLatency       float64 `json:"avg_latency_ms"`
	Status           string  `json:"status"`
	Error            string  `json:"error,omitempty"`
	DashboardEnabled bool    `json:"dashboard_enabled"`
	DashboardURL     string  `json:"dashboard_url,omitempty"`
}

type WorkerDashboardStatus struct {
	Name             string `json:"name"`
	Address          string `json:"address"`
	WorkerStatus     string `json:"worker_status"`
	Phase            string `json:"phase,omitempty"`
	DashboardEnabled bool   `json:"dashboard_enabled"`
	DashboardURL     string `json:"dashboard_url,omitempty"`
	Availability     string `json:"availability"`
	Message          string `json:"message,omitempty"`
	ActiveTestID     string `json:"active_test_id,omitempty"`
}

// TimePoint captures a snapshot of metrics at a specific time during the test.
// P99 is intentionally omitted — the k6 REST API does not provide it live.
// Real P99 values are captured via handleSummary after test completion.
type TimePoint struct {
	ElapsedSec       float64 `json:"t"`
	TotalVUs         int     `json:"vus"`
	RPS              float64 `json:"rps"`
	AvgLatency       float64 `json:"avg_ms"`
	P95Latency       float64 `json:"p95_ms"`
	TotalRequests    float64 `json:"reqs"`
	BusinessRequests float64 `json:"business_reqs,omitempty"`
	ErrorRate        float64 `json:"err_rate"`
	Status4xx        float64 `json:"status_4xx"`
	Status5xx        float64 `json:"status_5xx"`
}

// TestMetadata holds contextual information about the test run.
type TestMetadata struct {
	StartedAt   time.Time        `json:"started_at"`
	EndedAt     time.Time        `json:"ended_at"`
	DurationS   float64          `json:"duration_s"`
	WorkerCount int              `json:"worker_count"`
	Stages      []Stage          `json:"stages,omitempty"`
	ScriptURL   string           `json:"script_url,omitempty"`
	Payload     *PayloadMetadata `json:"payload,omitempty"`
	Auth        *AuthMetadata    `json:"auth,omitempty"`
}

type PayloadMetadata struct {
	HTTPMethod  string  `json:"http_method"`
	ContentType string  `json:"content_type"`
	TargetBytes int     `json:"payload_target_bytes"`
	TargetKiB   float64 `json:"payload_target_kib"`
	TargetKB    float64 `json:"payload_target_kb"`
	ActualBytes int     `json:"payload_actual_bytes"`
	ActualKiB   float64 `json:"payload_actual_kib"`
	ActualKB    float64 `json:"payload_actual_kb"`
}

type AuthMetadata struct {
	Mode               string              `json:"mode,omitempty"`
	TokenURL           string              `json:"token_url,omitempty"`
	ClientAuthMethod   string              `json:"client_auth_method,omitempty"`
	RefreshSkewSeconds int                 `json:"refresh_skew_seconds,omitempty"`
	SecretSource       string              `json:"secret_source,omitempty"`
	MetricsStatus      string              `json:"metrics_status,omitempty"`
	MetricsMessage     string              `json:"metrics_message,omitempty"`
	Metrics            *AuthRuntimeMetrics `json:"metrics,omitempty"`
}

type AuthRuntimeMetrics struct {
	TokenRequestsTotal   float64           `json:"token_requests_total"`
	TokenSuccessTotal    float64           `json:"token_success_total"`
	TokenFailureTotal    float64           `json:"token_failure_total"`
	TokenSuccessRate     float64           `json:"token_success_rate"`
	TokenRequestAvgMs    float64           `json:"token_request_avg_ms"`
	TokenRequestP95Ms    float64           `json:"token_request_p95_ms"`
	TokenRequestP99Ms    float64           `json:"token_request_p99_ms"`
	TokenRequestMaxMs    float64           `json:"token_request_max_ms"`
	TokenRefreshTotal    float64           `json:"token_refresh_total"`
	TokenReuseHitsTotal  float64           `json:"token_reuse_hits_total"`
	ResponseStatusCodes  []StatusCodeCount `json:"response_status_codes,omitempty"`
	AbortTriggered       bool              `json:"abort_triggered,omitempty"`
	AbortCause           string            `json:"abort_cause,omitempty"`
	AbortReason          string            `json:"abort_reason,omitempty"`
	AbortHTTPStatusCodes []int             `json:"abort_http_status_codes,omitempty"`
	AbortRetryable       bool              `json:"abort_retryable,omitempty"`
}

// --- Templates ---

type TestTemplate struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Mode             string     `json:"mode"` // "builder" or "upload"
	URL              string     `json:"url,omitempty"`
	Stages           []Stage    `json:"stages,omitempty"`
	ScriptContent    string     `json:"script_content,omitempty"`
	ConfigContent    string     `json:"config_content,omitempty"`
	HTTPMethod       string     `json:"http_method,omitempty"`
	ContentType      string     `json:"content_type,omitempty"`
	PayloadJSON      string     `json:"payload_json,omitempty"`
	PayloadTargetKiB int        `json:"payload_target_kib,omitempty"`
	AuthConfig       AuthConfig `json:"auth,omitempty"`
	Executor         string     `json:"executor,omitempty"`
	System           bool       `json:"system"`
	UserID           int64      `json:"user_id"`
	Username         string     `json:"username"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type TestTemplateRequest struct {
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Mode             string    `json:"mode"`
	URL              string    `json:"url,omitempty"`
	Stages           []Stage   `json:"stages,omitempty"`
	ScriptContent    string    `json:"script_content,omitempty"`
	ConfigContent    string    `json:"config_content,omitempty"`
	HTTPMethod       string    `json:"http_method,omitempty"`
	ContentType      string    `json:"content_type,omitempty"`
	PayloadJSON      string    `json:"payload_json,omitempty"`
	PayloadTargetKiB int       `json:"payload_target_kib,omitempty"`
	Auth             AuthInput `json:"auth,omitempty"`
}

// --- Scheduling ---

type ScheduledTest struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	ProjectName        string     `json:"project_name"`
	URL                string     `json:"url"`
	Mode               string     `json:"mode"`
	Executor           string     `json:"executor"`
	Stages             []Stage    `json:"stages,omitempty"`
	VUs                int        `json:"vus,omitempty"`
	Duration           string     `json:"duration,omitempty"`
	Rate               int        `json:"rate,omitempty"`
	TimeUnit           string     `json:"time_unit,omitempty"`
	PreAllocatedVUs    int        `json:"pre_allocated_vus,omitempty"`
	MaxVUs             int        `json:"max_vus,omitempty"`
	SleepSeconds       *float64   `json:"sleep_seconds,omitempty"`
	ScriptContent      string     `json:"script_content,omitempty"`
	ConfigContent      string     `json:"config_content,omitempty"`
	HTTPMethod         string     `json:"http_method,omitempty"`
	ContentType        string     `json:"content_type,omitempty"`
	PayloadJSON        string     `json:"payload_json,omitempty"`
	PayloadTargetKiB   int        `json:"payload_target_kib,omitempty"`
	AuthConfig         AuthConfig `json:"auth,omitempty"`
	ScheduledAt        time.Time  `json:"scheduled_at"`
	EstimatedDurationS int        `json:"estimated_duration_s"`
	Timezone           string     `json:"timezone"`
	RecurrenceType     string     `json:"recurrence_type"`
	RecurrenceRule     string     `json:"recurrence_rule,omitempty"`
	RecurrenceEnd      *time.Time `json:"recurrence_end,omitempty"`
	Status             string     `json:"status"`
	Paused             bool       `json:"paused"`
	UserID             int64      `json:"user_id"`
	Username           string     `json:"username"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// ToTestRequest converts a ScheduledTest to a TestRequest for execution.
func (s *ScheduledTest) ToTestRequest() TestRequest {
	return TestRequest{
		ProjectName:      s.ProjectName,
		URL:              s.URL,
		Executor:         s.Executor,
		Stages:           s.Stages,
		VUs:              s.VUs,
		Duration:         s.Duration,
		Rate:             s.Rate,
		TimeUnit:         s.TimeUnit,
		PreAllocatedVUs:  s.PreAllocatedVUs,
		MaxVUs:           s.MaxVUs,
		SleepSeconds:     s.SleepSeconds,
		ScriptContent:    s.ScriptContent,
		ConfigContent:    s.ConfigContent,
		HTTPMethod:       s.HTTPMethod,
		ContentType:      s.ContentType,
		PayloadJSON:      s.PayloadJSON,
		PayloadTargetKiB: s.PayloadTargetKiB,
		Auth: AuthInput{
			Enabled:            s.AuthConfig.Enabled,
			Mode:               s.AuthConfig.Mode,
			TokenURL:           s.AuthConfig.TokenURL,
			ClientID:           s.AuthConfig.ClientID,
			ClientAuthMethod:   s.AuthConfig.ClientAuthMethod,
			RefreshSkewSeconds: s.AuthConfig.RefreshSkewSeconds,
		},
	}
}

type ScheduleExecution struct {
	ID           string     `json:"id"`
	ScheduleID   string     `json:"schedule_id"`
	LoadTestID   *string    `json:"load_test_id,omitempty"`
	Status       string     `json:"status"`
	ScheduledAt  time.Time  `json:"scheduled_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	ErrorMessage *string    `json:"error_message,omitempty"`
	ErrorDetail  *string    `json:"error_detail,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type CreateScheduleRequest struct {
	Name               string    `json:"name"`
	ProjectName        string    `json:"project_name"`
	URL                string    `json:"url"`
	Mode               string    `json:"mode"`
	Executor           string    `json:"executor,omitempty"`
	Stages             []Stage   `json:"stages,omitempty"`
	VUs                int       `json:"vus,omitempty"`
	Duration           string    `json:"duration,omitempty"`
	Rate               int       `json:"rate,omitempty"`
	TimeUnit           string    `json:"time_unit,omitempty"`
	PreAllocatedVUs    int       `json:"pre_allocated_vus,omitempty"`
	MaxVUs             int       `json:"max_vus,omitempty"`
	SleepSeconds       *float64  `json:"sleep_seconds,omitempty"`
	ScriptContent      string    `json:"script_content,omitempty"`
	ConfigContent      string    `json:"config_content,omitempty"`
	HTTPMethod         string    `json:"http_method,omitempty"`
	ContentType        string    `json:"content_type,omitempty"`
	PayloadJSON        string    `json:"payload_json,omitempty"`
	PayloadTargetKiB   int       `json:"payload_target_kib,omitempty"`
	Auth               AuthInput `json:"auth,omitempty"`
	ScheduledAt        string    `json:"scheduled_at"`
	EstimatedDurationS int       `json:"estimated_duration_s,omitempty"`
	Timezone           string    `json:"timezone"`
	RecurrenceType     string    `json:"recurrence_type"`
	RecurrenceRule     string    `json:"recurrence_rule,omitempty"`
	RecurrenceEnd      *string   `json:"recurrence_end,omitempty"`
}

type CalendarEvent struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ProjectName    string `json:"project_name"`
	Start          string `json:"start"`
	End            string `json:"end"`
	Status         string `json:"status"`
	RecurrenceType string `json:"recurrence_type"`
	Username       string `json:"username"`
	UserID         int64  `json:"user_id"`
}

type ScheduleConflict struct {
	ScheduleID   string `json:"schedule_id,omitempty"`
	ScheduleName string `json:"schedule_name,omitempty"`
	Start        string `json:"start"`
	End          string `json:"end"`
	Type         string `json:"type"` // "running", "scheduled"
}

// --- Persisted test results ---

type TestResult struct {
	ID                 string             `json:"id"`
	ProjectName        string             `json:"project_name"`
	URL                string             `json:"url"`
	Status             string             `json:"status"`
	Metrics            *AggregatedMetrics `json:"metrics,omitempty"`
	MetricsV2          *MetricsV2         `json:"metrics_v2,omitempty"`
	TimeSeries         []TimePoint        `json:"time_series,omitempty"`
	Metadata           *TestMetadata      `json:"metadata,omitempty"`
	Warnings           []ConflictWarning  `json:"warnings,omitempty"`
	SummaryContent     string             `json:"summary_content,omitempty"`
	AuthSummaryContent string             `json:"auth_summary_content,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	Username           string             `json:"username"`
}
