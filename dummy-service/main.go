package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	startTime = time.Now()

	requestsStarted   atomic.Int64
	requestsCompleted atomic.Int64
	activeRequests    atomic.Int64
	activeSSE         atomic.Int64

	userIDCounter  atomic.Int64
	orderIDCounter atomic.Int64

	cfg Config

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dummy_http_requests_total",
			Help: "Total number of HTTP requests started.",
		},
		[]string{"method", "route"},
	)

	httpResponsesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dummy_http_responses_total",
			Help: "Total number of completed HTTP responses.",
		},
		[]string{"method", "route", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dummy_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	httpActiveRequestsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dummy_http_active_requests",
			Help: "Current number of active HTTP requests.",
		},
	)

	sseActiveConnectionsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dummy_sse_active_connections",
			Help: "Current number of active SSE connections.",
		},
	)

	sseEventsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "dummy_sse_events_total",
			Help: "Total number of SSE events sent.",
		},
	)

	authTokenRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dummy_auth_token_requests_total",
			Help: "Total number of auth token requests handled by the dummy service.",
		},
		[]string{"result"},
	)

	authTokenRequestDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "dummy_auth_token_request_duration_seconds",
			Help:    "Duration of auth token requests in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)

	authValidationFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dummy_auth_validation_failures_total",
			Help: "Total number of bearer token validation failures.",
		},
		[]string{"reason"},
	)
)

const maxCatchAllBodyBytes int64 = 16 << 20

type Config struct {
	Port                 string
	RequireAuth          bool
	StrictRoutes         bool
	DefaultStatusCode    int
	DefaultErrorRate     float64
	MinLatency           time.Duration
	MaxLatency           time.Duration
	SSEInterval          time.Duration
	ReadHeaderTimeout    time.Duration
	LogEveryNRequests    int64
	EnableRequestLogging bool
	AuthClientID         string
	AuthClientSecret     string
	AuthJWTSecret        string
	AuthIssuer           string
	AuthTokenTTL         time.Duration
	AuthTokenDelay       time.Duration
	AuthTokenForceStatus int
}

type StatsResponse struct {
	RequestsStarted   int64   `json:"requests_started"`
	RequestsCompleted int64   `json:"requests_completed"`
	ActiveRequests    int64   `json:"active_requests"`
	ActiveSSE         int64   `json:"active_sse"`
	UptimeSeconds     float64 `json:"uptime_seconds"`
}

type dummyJWTClaims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Aud string `json:"aud,omitempty"`
	Iat int64  `json:"iat"`
	Nbf int64  `json:"nbf"`
	Exp int64  `json:"exp"`
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rw *responseRecorder) WriteHeader(statusCode int) {
	if rw.wroteHeader {
		return
	}
	rw.status = statusCode
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseRecorder) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func main() {
	cfg = loadConfig()

	prometheus.MustRegister(
		httpRequestsTotal,
		httpResponsesTotal,
		httpRequestDuration,
		httpActiveRequestsGauge,
		sseActiveConnectionsGauge,
		sseEventsTotal,
		authTokenRequestsTotal,
		authTokenRequestDuration,
		authValidationFailuresTotal,
	)

	// deterministic IDs
	userIDCounter.Store(1000)
	orderIDCounter.Store(1000)

	mux := http.NewServeMux()

	// health and metrics
	mux.HandleFunc("GET /health", handleHealth)
	mux.Handle("GET /metrics", promhttp.Handler())

	// auth token
	mux.HandleFunc("GET /api/auth/token", handleToken)
	mux.HandleFunc("POST /api/auth/token", handleToken)
	mux.HandleFunc("/api/auth/token/{scenario}", handleTokenScenario)

	// stats
	mux.HandleFunc("GET /api/stats", handleStats)

	// SSE
	mux.HandleFunc("GET /api/events", handleSSE)

	// REST endpoints
	mux.HandleFunc("GET /api/users", handleGetUsers)
	mux.HandleFunc("GET /api/users/{id}", handleGetUserByID)
	mux.HandleFunc("POST /api/users", handleCreateUser)
	mux.HandleFunc("PUT /api/users/{id}", handleUpdateUser)
	mux.HandleFunc("DELETE /api/users/{id}", handleDeleteUser)

	mux.HandleFunc("GET /api/products", handleGetProducts)

	mux.HandleFunc("GET /api/orders", handleGetOrders)
	mux.HandleFunc("POST /api/orders", handleCreateOrder)
	mux.HandleFunc("GET /test/http/{scenario}", handleHTTPScenario)
	mux.HandleFunc("POST /test/http/{scenario}", handleHTTPScenario)
	mux.HandleFunc("PUT /test/http/{scenario}", handleHTTPScenario)
	mux.HandleFunc("PATCH /test/http/{scenario}", handleHTTPScenario)
	mux.HandleFunc("DELETE /test/http/{scenario}", handleHTTPScenario)

	if cfg.StrictRoutes {
		mux.HandleFunc("/", handleNotFound)
	} else {
		mux.HandleFunc("/", handleCatchAllOK)
	}

	handler := requestMiddleware(mux)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}

	log.Printf("dummy-service starting on :%s", cfg.Port)
	log.Printf("config: require_auth=%t strict_routes=%t default_status=%d error_rate=%.4f latency=%s..%s sse_interval=%s",
		cfg.RequireAuth,
		cfg.StrictRoutes,
		cfg.DefaultStatusCode,
		cfg.DefaultErrorRate,
		cfg.MinLatency,
		cfg.MaxLatency,
		cfg.SSEInterval,
	)

	log.Fatal(srv.ListenAndServe())
}

func loadConfig() Config {
	port := getEnv("PORT", "8090")
	requireAuth := getEnvBool("REQUIRE_AUTH", false)
	strictRoutes := getEnvBool("STRICT_ROUTES", false)
	defaultStatus := getEnvInt("DEFAULT_STATUS_CODE", http.StatusOK)
	defaultErrorRate := clamp(getEnvFloat("DEFAULT_ERROR_RATE", 0.0), 0.0, 1.0)

	minLatencyMs := getEnvInt("MIN_LATENCY_MS", 0)
	maxLatencyMs := getEnvInt("MAX_LATENCY_MS", 0)
	if maxLatencyMs < minLatencyMs {
		maxLatencyMs = minLatencyMs
	}

	sseIntervalMs := getEnvInt("SSE_INTERVAL_MS", 1000)
	if sseIntervalMs < 1 {
		sseIntervalMs = 1000
	}

	readHeaderTimeoutMs := getEnvInt("READ_HEADER_TIMEOUT_MS", 5000)
	if readHeaderTimeoutMs < 1 {
		readHeaderTimeoutMs = 5000
	}

	authTokenTTLSeconds := getEnvInt("AUTH_TOKEN_EXPIRES_SECONDS", 300)
	if authTokenTTLSeconds < 5 {
		authTokenTTLSeconds = 5
	}

	authTokenDelayMs := getEnvInt("AUTH_TOKEN_DELAY_MS", 0)
	if authTokenDelayMs < 0 {
		authTokenDelayMs = 0
	}

	logEveryNRequests := int64(getEnvInt("LOG_EVERY_N_REQUESTS", 1000))
	if logEveryNRequests < 1 {
		logEveryNRequests = 1000
	}

	return Config{
		Port:                 port,
		RequireAuth:          requireAuth,
		StrictRoutes:         strictRoutes,
		DefaultStatusCode:    defaultStatus,
		DefaultErrorRate:     defaultErrorRate,
		MinLatency:           time.Duration(minLatencyMs) * time.Millisecond,
		MaxLatency:           time.Duration(maxLatencyMs) * time.Millisecond,
		SSEInterval:          time.Duration(sseIntervalMs) * time.Millisecond,
		ReadHeaderTimeout:    time.Duration(readHeaderTimeoutMs) * time.Millisecond,
		LogEveryNRequests:    logEveryNRequests,
		EnableRequestLogging: getEnvBool("ENABLE_REQUEST_LOGGING", false),
		AuthClientID:         getEnv("AUTH_CLIENT_ID", "dummy-client"),
		AuthClientSecret:     getEnv("AUTH_CLIENT_SECRET", "dummy-secret"),
		AuthJWTSecret:        getEnv("AUTH_JWT_SECRET", "dummy-jwt-secret"),
		AuthIssuer:           getEnv("AUTH_ISSUER", "dummy-service"),
		AuthTokenTTL:         time.Duration(authTokenTTLSeconds) * time.Second,
		AuthTokenDelay:       time.Duration(authTokenDelayMs) * time.Millisecond,
		AuthTokenForceStatus: getEnvInt("AUTH_TOKEN_FORCE_STATUS_CODE", 0),
	}
}

func requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// health is intentionally excluded from custom counters/gauges to avoid skew from probes
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		route := routeLabel(r)
		method := r.Method

		requestsStarted.Add(1)
		activeRequests.Add(1)
		httpActiveRequestsGauge.Inc()
		httpRequestsTotal.WithLabelValues(method, route).Inc()

		if cfg.EnableRequestLogging {
			log.Printf("started method=%s path=%s route=%s remote=%s", r.Method, r.URL.Path, route, r.RemoteAddr)
		}

		start := time.Now()
		rec := &responseRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		defer func() {
			duration := time.Since(start)

			activeRequests.Add(-1)
			requestsCompleted.Add(1)
			httpActiveRequestsGauge.Dec()

			statusText := strconv.Itoa(rec.status)
			httpResponsesTotal.WithLabelValues(method, route, statusText).Inc()
			httpRequestDuration.WithLabelValues(method, route).Observe(duration.Seconds())

			n := requestsStarted.Load()
			if cfg.LogEveryNRequests > 0 && n%cfg.LogEveryNRequests == 0 {
				log.Printf(
					"requests_started=%d requests_completed=%d active_requests=%d active_sse=%d uptime=%.0fs",
					requestsStarted.Load(),
					requestsCompleted.Load(),
					activeRequests.Load(),
					activeSSE.Load(),
					time.Since(startTime).Seconds(),
				)
			}

			if cfg.EnableRequestLogging {
				log.Printf(
					"completed method=%s path=%s route=%s status=%d duration_ms=%d",
					r.Method, r.URL.Path, route, rec.status, duration.Milliseconds(),
				)
			}
		}()

		next.ServeHTTP(rec, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func handleToken(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := "success"
	defer func() {
		authTokenRequestsTotal.WithLabelValues(result).Inc()
		authTokenRequestDuration.Observe(time.Since(start).Seconds())
	}()

	delayTokenIfNeeded(r)

	if status := effectiveTokenFailureStatus(r); status > 0 {
		result = "forced_failure"
		writeJSON(w, status, map[string]any{
			"error":             "server_error",
			"error_description": "dummy token endpoint forced failure",
		})
		return
	}

	clientID, clientSecret, grantType, err := extractTokenRequestCredentials(r)
	if err != nil {
		result = "invalid_request"
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":             "invalid_request",
			"error_description": err.Error(),
		})
		return
	}

	if grantType != "client_credentials" {
		result = "unsupported_grant_type"
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":             "unsupported_grant_type",
			"error_description": "grant_type must be client_credentials",
		})
		return
	}

	if clientID != cfg.AuthClientID || clientSecret != cfg.AuthClientSecret {
		result = "invalid_client"
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":             "invalid_client",
			"error_description": "client credentials are invalid",
		})
		return
	}

	token, expiresAt, err := issueDummyJWT(clientID, time.Now().UTC())
	if err != nil {
		result = "token_issue_error"
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":             "server_error",
			"error_description": "failed to issue token",
		})
		return
	}

	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(time.Until(expiresAt).Seconds()),
	})
}

func handleTokenScenario(w http.ResponseWriter, r *http.Request) {
	scenario := strings.TrimSpace(strings.ToLower(r.PathValue("scenario")))
	if scenario == "timeout" {
		handleIntentionalTimeout(r)
		return
	}

	code, err := strconv.Atoi(scenario)
	if err != nil || code < 100 || code > 599 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":             "invalid_scenario",
			"error_description": "scenario must be an HTTP status code or timeout",
		})
		return
	}

	if code >= 300 && code < 400 {
		w.Header().Set("Location", "/api/auth/token")
	}

	writeJSON(w, code, map[string]any{
		"error":             "dummy_auth_scenario",
		"error_description": fmt.Sprintf("forced dummy auth response %d", code),
		"scenario":          scenario,
	})
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, StatsResponse{
		RequestsStarted:   requestsStarted.Load(),
		RequestsCompleted: requestsCompleted.Load(),
		ActiveRequests:    activeRequests.Load(),
		ActiveSSE:         activeSSE.Load(),
		UptimeSeconds:     time.Since(startTime).Seconds(),
	})
}

func handleSSE(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	if shouldFail(r) {
		writeJSON(w, effectiveStatusCode(r, http.StatusServiceUnavailable), map[string]string{
			"error": "simulated SSE failure",
		})
		return
	}

	delayIfNeeded(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	n := activeSSE.Add(1)
	sseActiveConnectionsGauge.Inc()
	log.Printf("SSE connect active=%d", n)

	defer func() {
		n := activeSSE.Add(-1)
		sseActiveConnectionsGauge.Dec()
		log.Printf("SSE disconnect active=%d", n)
	}()

	// Initial handshake event so clients can confirm the stream is alive immediately.
	io.WriteString(w, "retry: 3000\n")
	io.WriteString(w, "event: ready\n")
	io.WriteString(w, "data: {\"status\":\"connected\"}\n\n")
	flusher.Flush()
	sseEventsTotal.Inc()

	ticker := time.NewTicker(getSSEInterval(r))
	defer ticker.Stop()

	seq := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case t := <-ticker.C:
			seq++
			value := deterministicValue(seq)
			fmt.Fprintf(
				w,
				"id: %d\nevent: tick\ndata: {\"seq\":%d,\"ts\":\"%s\",\"value\":%.2f}\n\n",
				seq,
				seq,
				t.UTC().Format(time.RFC3339),
				value,
			)
			flusher.Flush()
			sseEventsTotal.Inc()
		}
	}
}

func handleGetUsers(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]any{
		"users": []map[string]any{
			{"id": 1, "name": "Alice", "email": "alice@example.com"},
			{"id": 2, "name": "Bob", "email": "bob@example.com"},
			{"id": 3, "name": "Charlie", "email": "charlie@example.com"},
		},
		"total": 3,
	})
}

func handleGetUserByID(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	id := r.PathValue("id")
	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]any{
		"id":    id,
		"name":  "User-" + id,
		"email": "user" + id + "@example.com",
	})
}

func handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	requestBodyBytes, err := consumeRequestBody(r, maxCatchAllBodyBytes)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "exceeds") {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	id := userIDCounter.Add(1)
	writeJSON(w, effectiveStatusCode(r, http.StatusCreated), map[string]any{
		"id":                 id,
		"status":             "created",
		"request_body_bytes": requestBodyBytes,
	})
}

func handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	requestBodyBytes, err := consumeRequestBody(r, maxCatchAllBodyBytes)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "exceeds") {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	id := r.PathValue("id")
	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]any{
		"id":                 id,
		"status":             "updated",
		"request_body_bytes": requestBodyBytes,
	})
}

func handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]string{"status": "deleted"})
}

func handleGetProducts(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]any{
		"products": []map[string]any{
			{"id": 1, "name": "Widget", "price": 9.99},
			{"id": 2, "name": "Gadget", "price": 24.99},
			{"id": 3, "name": "Doohickey", "price": 14.50},
		},
		"total": 3,
	})
}

func handleGetOrders(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]any{
		"orders": []map[string]any{
			{"id": 1001, "user_id": 1, "total": 34.98, "status": "shipped"},
			{"id": 1002, "user_id": 2, "total": 9.99, "status": "pending"},
		},
		"total": 2,
	})
}

func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	requestBodyBytes, err := consumeRequestBody(r, maxCatchAllBodyBytes)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "exceeds") {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	id := orderIDCounter.Add(1)
	writeJSON(w, effectiveStatusCode(r, http.StatusCreated), map[string]any{
		"id":                 id,
		"status":             "created",
		"request_body_bytes": requestBodyBytes,
	})
}

func handleHTTPScenario(w http.ResponseWriter, r *http.Request) {
	if err := maybeRequireAuth(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	scenario := strings.TrimSpace(strings.ToLower(r.PathValue("scenario")))
	if scenario == "timeout" {
		handleIntentionalTimeout(r)
		return
	}

	code, err := strconv.Atoi(scenario)
	if err != nil || code < 100 || code > 599 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "error",
			"message": "scenario must be an HTTP status code or timeout",
			"path":    r.URL.Path,
		})
		return
	}

	if code >= 300 && code < 400 {
		w.Header().Set("Location", "/api/products")
	}

	writeJSON(w, code, map[string]any{
		"status":   "scenario",
		"code":     code,
		"method":   r.Method,
		"path":     r.URL.Path,
		"scenario": scenario,
		"message":  fmt.Sprintf("forced dummy response %d", code),
	})
}

func handleCatchAllOK(w http.ResponseWriter, r *http.Request) {
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	requestBodyBytes, err := consumeRequestBody(r, maxCatchAllBodyBytes)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "exceeds") {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]any{
			"status":  "error",
			"method":  r.Method,
			"path":    r.URL.Path,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, effectiveStatusCode(r, http.StatusOK), map[string]any{
		"status":             "ok",
		"method":             r.Method,
		"path":               r.URL.Path,
		"message":            "dummy response",
		"request_body_bytes": requestBodyBytes,
	})
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	if respondIfFailureInjected(w, r) {
		return
	}
	delayIfNeeded(r)

	writeJSON(w, effectiveStatusCode(r, http.StatusNotFound), map[string]any{
		"status":  "error",
		"method":  r.Method,
		"path":    r.URL.Path,
		"message": "route not found",
	})
}

func handleIntentionalTimeout(r *http.Request) {
	select {
	case <-r.Context().Done():
		return
	case <-time.After(45 * time.Second):
		return
	}
}

func consumeRequestBody(r *http.Request, maxBytes int64) (int64, error) {
	if r.Body == nil {
		return 0, nil
	}
	defer r.Body.Close()

	limited := &io.LimitedReader{R: r.Body, N: maxBytes + 1}
	readBytes, err := io.Copy(io.Discard, limited)
	if err != nil {
		return readBytes, fmt.Errorf("failed to read request body: %w", err)
	}
	if readBytes > maxBytes {
		return readBytes, fmt.Errorf("request body exceeds %d bytes", maxBytes)
	}
	return readBytes, nil
}

func maybeRequireAuth(r *http.Request) error {
	if !cfg.RequireAuth {
		return nil
	}
	return validateBearerToken(r.Context(), r.Header.Get("Authorization"))
}

func validateBearerToken(_ context.Context, header string) error {
	if header == "" {
		authValidationFailuresTotal.WithLabelValues("missing_header").Inc()
		return errors.New("missing Authorization header")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		authValidationFailuresTotal.WithLabelValues("invalid_scheme").Inc()
		return errors.New("Authorization header must use Bearer token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		authValidationFailuresTotal.WithLabelValues("empty_token").Inc()
		return errors.New("Bearer token is empty")
	}
	claims, err := parseAndValidateDummyJWT(token, time.Now().UTC())
	if err != nil {
		authValidationFailuresTotal.WithLabelValues("invalid_token").Inc()
		return err
	}
	if claims.Iss != cfg.AuthIssuer {
		authValidationFailuresTotal.WithLabelValues("invalid_issuer").Inc()
		return errors.New("invalid token issuer")
	}
	return nil
}

func extractTokenRequestCredentials(r *http.Request) (string, string, string, error) {
	clientID, clientSecret, hasBasic, err := extractBasicAuthCredentials(r.Header.Get("Authorization"))
	if err != nil {
		return "", "", "", err
	}

	if err := r.ParseForm(); err != nil {
		return "", "", "", fmt.Errorf("failed to parse token request: %w", err)
	}

	grantType := strings.TrimSpace(r.Form.Get("grant_type"))
	if grantType == "" {
		grantType = strings.TrimSpace(r.URL.Query().Get("grant_type"))
	}
	if grantType == "" {
		grantType = "client_credentials"
	}

	if !hasBasic {
		clientID = strings.TrimSpace(r.Form.Get("client_id"))
		clientSecret = strings.TrimSpace(r.Form.Get("client_secret"))
		if clientID == "" {
			clientID = strings.TrimSpace(r.URL.Query().Get("client_id"))
		}
		if clientSecret == "" {
			clientSecret = strings.TrimSpace(r.URL.Query().Get("client_secret"))
		}
	}

	if clientID == "" || clientSecret == "" {
		return "", "", "", errors.New("client_id and client_secret are required")
	}

	return clientID, clientSecret, grantType, nil
}

func extractBasicAuthCredentials(header string) (string, string, bool, error) {
	const prefix = "Basic "
	if strings.TrimSpace(header) == "" {
		return "", "", false, nil
	}
	if !strings.HasPrefix(header, prefix) {
		return "", "", false, nil
	}
	raw := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if raw == "" {
		return "", "", false, errors.New("Basic authorization header is empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", "", false, errors.New("Basic authorization header is not valid base64")
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", false, errors.New("Basic authorization header must contain client_id:client_secret")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true, nil
}

func issueDummyJWT(subject string, now time.Time) (string, time.Time, error) {
	expiresAt := now.Add(cfg.AuthTokenTTL)
	claims := dummyJWTClaims{
		Iss: cfg.AuthIssuer,
		Sub: subject,
		Iat: now.Unix(),
		Nbf: now.Unix(),
		Exp: expiresAt.Unix(),
	}

	headerJSON, err := json.Marshal(map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	})
	if err != nil {
		return "", time.Time{}, err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}

	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsPart := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerPart + "." + claimsPart
	signature := signJWT(signingInput)

	return signingInput + "." + signature, expiresAt, nil
}

func parseAndValidateDummyJWT(token string, now time.Time) (dummyJWTClaims, error) {
	var claims dummyJWTClaims

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, errors.New("invalid Bearer token")
	}

	signingInput := parts[0] + "." + parts[1]
	expectedSignature := signJWT(signingInput)
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSignature)) {
		return claims, errors.New("invalid Bearer token signature")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, errors.New("invalid Bearer token payload")
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return claims, errors.New("invalid Bearer token claims")
	}
	if claims.Exp == 0 {
		return claims, errors.New("Bearer token missing exp")
	}
	if now.Unix() >= claims.Exp {
		return claims, errors.New("Bearer token expired")
	}
	if claims.Nbf > 0 && now.Unix() < claims.Nbf {
		return claims, errors.New("Bearer token not valid yet")
	}

	return claims, nil
}

func signJWT(signingInput string) string {
	mac := hmac.New(sha256.New, []byte(cfg.AuthJWTSecret))
	_, _ = mac.Write([]byte(signingInput))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func delayTokenIfNeeded(r *http.Request) {
	if raw := r.URL.Query().Get("delay_ms"); raw != "" {
		delayIfNeeded(r)
		return
	}
	if cfg.AuthTokenDelay > 0 {
		time.Sleep(cfg.AuthTokenDelay)
		return
	}
	delayIfNeeded(r)
}

func effectiveTokenFailureStatus(r *http.Request) int {
	if raw := r.URL.Query().Get("status"); raw != "" {
		if code, err := strconv.Atoi(raw); err == nil && code >= 500 && code <= 599 {
			return code
		}
	}
	if raw := r.URL.Query().Get("fail"); raw != "" && shouldFail(r) {
		return effectiveStatusCode(r, http.StatusServiceUnavailable)
	}
	if cfg.AuthTokenForceStatus >= 500 && cfg.AuthTokenForceStatus <= 599 {
		return cfg.AuthTokenForceStatus
	}
	return 0
}

func delayIfNeeded(r *http.Request) {
	delay := effectiveDelay(r)
	if delay > 0 {
		time.Sleep(delay)
	}
}

func effectiveDelay(r *http.Request) time.Duration {
	// request-specific override: ?delay_ms=25
	if raw := r.URL.Query().Get("delay_ms"); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}

	min := cfg.MinLatency
	max := cfg.MaxLatency

	if raw := r.URL.Query().Get("min_delay_ms"); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms >= 0 {
			min = time.Duration(ms) * time.Millisecond
		}
	}
	if raw := r.URL.Query().Get("max_delay_ms"); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms >= 0 {
			max = time.Duration(ms) * time.Millisecond
		}
	}

	if max < min {
		max = min
	}
	if min == max {
		return min
	}

	// Deterministic delay derived from nanoseconds modulo span would not be stable enough;
	// a midpoint keeps behavior cheap and predictable when using a range via env vars.
	return min + (max-min)/2
}

func effectiveStatusCode(r *http.Request, fallback int) int {
	if raw := r.URL.Query().Get("status"); raw != "" {
		if code, err := strconv.Atoi(raw); err == nil && code >= 100 && code <= 999 {
			return code
		}
	}
	if cfg.DefaultStatusCode >= 100 && cfg.DefaultStatusCode <= 999 {
		return cfg.DefaultStatusCode
	}
	return fallback
}

func shouldFail(r *http.Request) bool {
	// explicit failure flag wins
	if raw := r.URL.Query().Get("fail"); raw != "" {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}

	errorRate := cfg.DefaultErrorRate
	if raw := r.URL.Query().Get("error_rate"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			errorRate = clamp(v, 0.0, 1.0)
		}
	}

	if errorRate <= 0 {
		return false
	}
	if errorRate >= 1 {
		return true
	}

	// deterministic pseudo-decision by hashing a stable subset of request attributes
	seed := r.Method + "|" + r.URL.Path + "|" + r.URL.RawQuery
	score := deterministicProbability(seed)
	return score < errorRate
}

func respondIfFailureInjected(w http.ResponseWriter, r *http.Request) bool {
	if !shouldFail(r) {
		return false
	}
	status := effectiveStatusCode(r, http.StatusServiceUnavailable)
	writeJSON(w, status, map[string]any{
		"status":  "error",
		"method":  r.Method,
		"path":    r.URL.Path,
		"message": "simulated failure",
	})
	return true
}

func routeLabel(r *http.Request) string {
	pattern := r.Pattern
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func getSSEInterval(r *http.Request) time.Duration {
	if raw := r.URL.Query().Get("sse_interval_ms"); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return cfg.SSEInterval
}

func deterministicValue(seq int) float64 {
	// stable, cheap oscillation-like number in range 0..100
	v := math.Mod(float64(seq*37), 100)
	return v + 0.37
}

func deterministicProbability(input string) float64 {
	var sum uint64
	for i := 0; i < len(input); i++ {
		sum = sum*131 + uint64(input[i])
	}
	return float64(sum%10000) / 10000.0
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func getEnv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getEnvBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func getEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func getEnvFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
