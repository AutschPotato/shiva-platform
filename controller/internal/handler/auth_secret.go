package handler

import (
	"fmt"
	"strings"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/secrets"
)

func buildStoredAuthConfig(input model.AuthInput, existing *model.AuthConfig, secretSvc *secrets.Service, requireStoredSecret bool) (model.AuthConfig, error) {
	cfg := authConfigFromStoredInput(input, existing)
	if !cfg.Enabled {
		return cfg, nil
	}

	if secretSvc == nil {
		return model.AuthConfig{}, fmt.Errorf("secret encryption service is not configured")
	}

	secretValue := strings.TrimSpace(input.ClientSecret)

	if input.ClearSecret {
		cfg.ClientSecretEncrypted = ""
		cfg.SecretConfigured = false
		cfg.SecretSource = ""
	}

	if secretValue != "" {
		enc, err := secretSvc.Encrypt(secretValue)
		if err != nil {
			return model.AuthConfig{}, fmt.Errorf("encrypt auth secret: %w", err)
		}
		cfg.ClientSecretEncrypted = enc
		cfg.SecretConfigured = true
		cfg.SecretSource = "persisted_encrypted"
	}

	shouldPersist := requireStoredSecret || input.PersistSecret
	if !shouldPersist && secretValue == "" && existing == nil {
		cfg.ClientSecretEncrypted = ""
		cfg.SecretConfigured = false
		cfg.SecretSource = ""
	}

	if requireStoredSecret && cfg.ClientSecretEncrypted == "" {
		return model.AuthConfig{}, fmt.Errorf("auth_client_secret is required when auth is enabled")
	}

	if input.PersistSecret && cfg.ClientSecretEncrypted == "" {
		return model.AuthConfig{}, fmt.Errorf("auth_client_secret is required when auth_persist_secret is true")
	}

	return cfg, nil
}
