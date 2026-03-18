package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DiscoveryMode describes how k6 workers are discovered.
type DiscoveryMode string

const (
	// DiscoveryExplicit uses a comma-separated list of host:port addresses.
	// Set via K6_WORKERS env var. Used in Docker Compose.
	DiscoveryExplicit DiscoveryMode = "explicit"

	// DiscoveryStatefulSet builds addresses from a Kubernetes StatefulSet
	// naming convention: {name}-{0..N-1}.{service}.{namespace}.svc.cluster.local:{port}
	// Set via K6_WORKER_STATEFULSET, K6_WORKER_REPLICAS, etc.
	DiscoveryStatefulSet DiscoveryMode = "statefulset"
)

type Config struct {
	Port       int
	DBUser     string
	DBPassword string
	DBHost     string
	DBPort     int
	DBName     string

	JWTSecret string
	APIKey    string
	AdminKey  string

	InitialAdminUsername string
	InitialAdminEmail    string
	InitialAdminPassword string

	// Workers holds the resolved list of worker addresses (host:port).
	Workers       []string
	WorkerDiscovery DiscoveryMode

	CORSOrigins []string

	MetricsPollIntervalMS int
	MaxTestDurationMin    int
	K6CompletionBufferSec int
	K6DashboardEnabled    bool
	K6DashboardHost       string
	K6DashboardPort       int

	PublicAppURL              string
	PasswordResetTokenTTLMin  int
	SMTPHost                  string
	SMTPPort                  int
	SMTPUser                  string
	SMTPPassword              string
	SMTPFromEmail             string
	SMTPFromName              string
	EncryptionKey             string

	ScriptsDir string
	OutputDir  string
}

func Load() (*Config, error) {
	c := &Config{
		Port:       envInt("PORT", 8080),
		DBUser:     envStr("DB_USER", "k6user"),
		DBPassword: envStr("DB_PASSWORD", "k6pass"),
		DBHost:     envStr("DB_HOST", "mysql"),
		DBPort:     envInt("DB_PORT", 3306),
		DBName:     envStr("DB_NAME", "shiva"),

		JWTSecret: envStr("JWT_SECRET", "change-me-in-production"),
		APIKey:    envStr("API_KEY", ""),
		AdminKey:  envStr("ADMIN_KEY", ""),

		InitialAdminUsername: envStr("INITIAL_ADMIN_USERNAME", "admin"),
		InitialAdminEmail:    envStr("INITIAL_ADMIN_EMAIL", "admin@example.com"),
		InitialAdminPassword: envStr("INITIAL_ADMIN_PASSWORD", "changeme"),

		CORSOrigins: strings.Split(envStr("CORS_ORIGINS", "http://localhost:3000"), ","),

		MetricsPollIntervalMS: envInt("METRICS_POLL_INTERVAL_MS", 2000),
		MaxTestDurationMin:    envInt("MAX_TEST_DURATION_MIN", 120),
		K6CompletionBufferSec: envInt("K6_COMPLETION_BUFFER_SEC", 30),
		K6DashboardEnabled:    envBool("K6_DASHBOARD_ENABLED", false),
		K6DashboardHost:       envStr("K6_DASHBOARD_HOST", "0.0.0.0"),
		K6DashboardPort:       envInt("K6_DASHBOARD_PORT", 5665),
		PublicAppURL:          envStr("PUBLIC_APP_URL", "http://localhost:3000"),
		PasswordResetTokenTTLMin: envInt("PASSWORD_RESET_TOKEN_TTL_MIN", 30),
		SMTPHost:              envStr("SMTP_HOST", ""),
		SMTPPort:              envInt("SMTP_PORT", 587),
		SMTPUser:              envStr("SMTP_USER", ""),
		SMTPPassword:          envStr("SMTP_PASSWORD", ""),
		SMTPFromEmail:         envStr("SMTP_FROM_EMAIL", "noreply@example.com"),
		SMTPFromName:          envStr("SMTP_FROM_NAME", "Shiva"),
		EncryptionKey:         envStr("APP_ENCRYPTION_KEY", envStr("JWT_SECRET", "change-me-in-production")),

		ScriptsDir: envStr("SCRIPTS_DIR", "/scripts"),
		OutputDir:  envStr("OUTPUT_DIR", "/output"),
	}

	c.Workers, c.WorkerDiscovery = resolveWorkers()

	if len(c.Workers) == 0 {
		return c, fmt.Errorf("no workers configured: set K6_WORKERS or K6_WORKER_STATEFULSET + K6_WORKER_REPLICAS")
	}

	if c.JWTSecret == "change-me-in-production" {
		return c, fmt.Errorf("JWT_SECRET must be set in production")
	}

	return c, nil
}

// resolveWorkers determines the worker list based on available environment variables.
//
// Priority:
//  1. K6_WORKERS (explicit comma-separated list) — Docker Compose, manual setups
//  2. K6_WORKER_STATEFULSET + K6_WORKER_REPLICAS — Kubernetes StatefulSet pattern
//
// For Kubernetes StatefulSet discovery, the following env vars are used:
//
//	K6_WORKER_STATEFULSET  — StatefulSet name (e.g. "shiva-worker")
//	K6_WORKER_REPLICAS     — number of replicas (e.g. 10)
//	K6_WORKER_PORT         — k6 API port per pod (default: 6565)
//	K6_WORKER_NAMESPACE    — Kubernetes namespace (default: "default")
//	K6_WORKER_CLUSTER_DOMAIN — cluster DNS suffix (default: "cluster.local")
//
// The resulting addresses follow the StatefulSet DNS convention:
//
//	{name}-0.{name}.{namespace}.svc.{domain}:{port}
//	{name}-1.{name}.{namespace}.svc.{domain}:{port}
//	...
func resolveWorkers() ([]string, DiscoveryMode) {
	// Mode 1: Explicit list (Docker Compose, manual)
	if explicit := os.Getenv("K6_WORKERS"); explicit != "" {
		addrs := splitAndTrim(explicit, ",")
		if len(addrs) > 0 {
			return addrs, DiscoveryExplicit
		}
	}

	// Mode 2: Kubernetes StatefulSet pattern
	stsName := envStr("K6_WORKER_STATEFULSET", "")
	replicas := envInt("K6_WORKER_REPLICAS", 0)
	if stsName != "" && replicas > 0 {
		port := envInt("K6_WORKER_PORT", 6565)
		namespace := envStr("K6_WORKER_NAMESPACE", "default")
		domain := envStr("K6_WORKER_CLUSTER_DOMAIN", "cluster.local")

		// Headless service typically has the same name as the StatefulSet.
		// Override with K6_WORKER_SERVICE if they differ.
		serviceName := envStr("K6_WORKER_SERVICE", stsName)

		workers := make([]string, replicas)
		for i := 0; i < replicas; i++ {
			// DNS: {sts}-{ordinal}.{service}.{namespace}.svc.{domain}:{port}
			workers[i] = fmt.Sprintf("%s-%d.%s.%s.svc.%s:%d",
				stsName, i, serviceName, namespace, domain, port)
		}
		return workers, DiscoveryStatefulSet
	}

	return nil, ""
}

func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}
