package scheduler

import (
	"log/slog"
	"testing"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/secrets"
)

func TestScheduleTestRequestDecryptsStoredSecret(t *testing.T) {
	secretSvc, err := secrets.NewService("test-encryption-key")
	if err != nil {
		t.Fatalf("failed to create secret service: %v", err)
	}
	encrypted, err := secretSvc.Encrypt("super-secret")
	if err != nil {
		t.Fatalf("failed to encrypt test secret: %v", err)
	}

	s := &Scheduler{
		logger:    slog.Default(),
		secretSvc: secretSvc,
	}

	schedule := &model.ScheduledTest{
		ID:          "sched-1",
		Name:        "Auth schedule",
		ProjectName: "demo",
		URL:         "https://api.example.com/orders",
		Mode:        "builder",
		Executor:    "constant-vus",
		Duration:    "1m",
		AuthConfig: model.AuthConfig{
			Enabled:               true,
			Mode:                  "oauth_client_credentials",
			TokenURL:              "https://auth.example.com/token",
			ClientID:              "client-1",
			ClientAuthMethod:      "basic",
			RefreshSkewSeconds:    30,
			ClientSecretEncrypted: encrypted,
		},
		ScheduledAt: time.Now(),
	}

	req, err := s.scheduleTestRequest(schedule)
	if err != nil {
		t.Fatalf("expected decrypted test request, got error: %v", err)
	}
	if req.Auth.ClientSecret != "super-secret" {
		t.Fatalf("expected decrypted client secret, got %q", req.Auth.ClientSecret)
	}
	if req.Auth.TokenURL != schedule.AuthConfig.TokenURL {
		t.Fatalf("expected auth token url to be preserved")
	}
}

func TestScheduleTestRequestFailsWhenAuthSecretMissing(t *testing.T) {
	s := &Scheduler{
		logger: slog.Default(),
	}

	schedule := &model.ScheduledTest{
		ID:          "sched-2",
		Name:        "broken auth schedule",
		ProjectName: "demo",
		URL:         "https://api.example.com/orders",
		Mode:        "builder",
		Executor:    "constant-vus",
		Duration:    "1m",
		AuthConfig: model.AuthConfig{
			Enabled:            true,
			Mode:               "oauth_client_credentials",
			TokenURL:           "https://auth.example.com/token",
			ClientID:           "client-1",
			ClientAuthMethod:   "basic",
			RefreshSkewSeconds: 30,
			SecretConfigured:   true,
			SecretSource:       "persisted_encrypted",
		},
		ScheduledAt: time.Now(),
	}

	if _, err := s.scheduleTestRequest(schedule); err == nil {
		t.Fatalf("expected missing scheduled auth secret to fail")
	}
}
