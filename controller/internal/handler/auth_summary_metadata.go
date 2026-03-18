package handler

import (
	"strings"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
)

func applyAuthSummaryToMetadata(metadata *model.TestMetadata, authSummary *scriptgen.AuthSummaryData) {
	if metadata == nil || authSummary == nil {
		return
	}
	if metadata.Auth == nil {
		metadata.Auth = &model.AuthMetadata{}
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
	if strings.TrimSpace(rawAuthSummary) == "" {
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
