package handler

import (
	"strings"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
)

func hasConfiguredAuth(metadata *model.TestMetadata) bool {
	if metadata == nil || metadata.Auth == nil {
		return false
	}
	auth := metadata.Auth
	return auth.Mode != "" ||
		auth.TokenURL != "" ||
		auth.ClientAuthMethod != "" ||
		auth.SecretSource != "" ||
		auth.RefreshSkewSeconds > 0
}

func applyAuthSummaryToMetadata(metadata *model.TestMetadata, authSummary *scriptgen.AuthSummaryData) {
	if metadata == nil || authSummary == nil || !hasConfiguredAuth(metadata) {
		return
	}
	if metadata.Auth.Mode == "" {
		metadata.Auth.Mode = authSummary.Mode
	}
	if metadata.Auth.TokenURL == "" {
		metadata.Auth.TokenURL = normalizeAuthTokenURL(authSummary.TokenURL)
	}
	if metadata.Auth.ClientAuthMethod == "" {
		metadata.Auth.ClientAuthMethod = authSummary.ClientAuthMethod
	}
	if metadata.Auth.RefreshSkewSeconds == 0 {
		metadata.Auth.RefreshSkewSeconds = authSummary.RefreshSkewSeconds
	}
	if metadata.Auth.MetricsStatus == "" {
		if authSummary.Status != "" {
			metadata.Auth.MetricsStatus = authSummary.Status
		} else {
			metadata.Auth.MetricsStatus = "complete"
		}
	}
	if metadata.Auth.MetricsMessage == "" {
		metadata.Auth.MetricsMessage = authSummary.Message
	}
	metrics := authSummary.Metrics
	metadata.Auth.Metrics = &metrics
}

func hydrateAuthMetadataFromRawSummary(metadata *model.TestMetadata, rawAuthSummary string) *model.TestMetadata {
	if strings.TrimSpace(rawAuthSummary) == "" || !hasConfiguredAuth(metadata) {
		return metadata
	}

	authSummary, err := scriptgen.ParseRawAuthSummaryContent(rawAuthSummary)
	if err != nil {
		return metadata
	}

	if metadata == nil {
		metadata = &model.TestMetadata{}
	}
	applyAuthSummaryToMetadata(metadata, authSummary)
	return metadata
}
