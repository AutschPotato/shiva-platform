package handler

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/shiva-load-testing/controller/internal/completion"
	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
)

func mergeResultArtifacts(result model.TestResult, snapshot completion.Snapshot, payloadFallback string) (model.TestResult, string, bool) {
	changed := false
	payloadContent := strings.TrimSpace(completion.FirstArtifactContent(snapshot, completion.ArtifactPayload))
	if payloadContent == "" {
		payloadContent = strings.TrimSpace(payloadFallback)
	}

	if result.Metadata == nil {
		result.Metadata = &model.TestMetadata{}
	}

	rawSummary := completion.BuildRawSummary(snapshot, completion.ArtifactSummary)
	if strings.TrimSpace(rawSummary) != "" {
		summaryResult, err := summaryCollectionFromRaw(snapshot.ExpectedWorkers, rawSummary, result.Metrics)
		if err == nil {
			if result.SummaryContent != summaryResult.Raw {
				result.SummaryContent = summaryResult.Raw
				changed = true
			}
			if !reflect.DeepEqual(result.Metadata.ArtifactCollection, summaryResult.ArtifactCollection) {
				result.Metadata.ArtifactCollection = summaryResult.ArtifactCollection
				changed = true
			}
		}
	} else {
		collection := classifyArtifactCollection(snapshot.ExpectedWorkers, nil)
		if !reflect.DeepEqual(result.Metadata.ArtifactCollection, collection) {
			result.Metadata.ArtifactCollection = collection
			changed = true
		}
	}

	if payloadContent != "" {
		before := result.Metadata.Payload
		refreshPayloadMetadata(result.Metadata, payloadContent)
		if !reflect.DeepEqual(before, result.Metadata.Payload) {
			changed = true
		}
	}

	rawAuthSummary := completion.BuildRawSummary(snapshot, completion.ArtifactAuthSummary)
	if strings.TrimSpace(rawAuthSummary) != "" {
		authSummary, err := scriptgen.ParseRawAuthSummaryContent(rawAuthSummary)
		if err == nil {
			if result.AuthSummaryContent != rawAuthSummary {
				result.AuthSummaryContent = rawAuthSummary
				changed = true
			}
			beforeAuth := result.Metadata.Auth
			applyAuthSummaryToMetadata(result.Metadata, authSummary)
			if !reflect.DeepEqual(beforeAuth, result.Metadata.Auth) {
				changed = true
			}
		}
	}

	if changed {
		result.MetricsV2 = buildMetricsV2(result.Metrics, result.SummaryContent, result.Metadata)
	}

	return result, payloadContent, changed
}

func mergeRegistryArtifactsIntoResult(result model.TestResult, registry *completion.Registry, testID string, payloadFallback string) (model.TestResult, string, bool) {
	if registry == nil {
		return result, strings.TrimSpace(payloadFallback), false
	}

	snapshot, ok := registry.Snapshot(testID)
	if !ok {
		return result, strings.TrimSpace(payloadFallback), false
	}

	return mergeResultArtifacts(result, snapshot, payloadFallback)
}

func (h *TestHandler) enrichCompletedResultFromRegistry(ctx context.Context, testID string) error {
	if h == nil || h.store == nil || h.completionRegistry == nil {
		return nil
	}

	snapshot, ok := h.completionRegistry.Snapshot(testID)
	if !ok {
		return nil
	}

	lt, err := h.store.GetLoadTest(ctx, testID)
	if err != nil || lt == nil {
		return err
	}
	if lt.Status != "completed" || len(lt.ResultJSON) == 0 {
		return nil
	}

	var result model.TestResult
	if err := json.Unmarshal(lt.ResultJSON, &result); err != nil {
		return err
	}

	enriched, payloadContent, changed := mergeResultArtifacts(result, snapshot, lt.PayloadContent)
	if !changed {
		return nil
	}

	resultJSON, err := json.Marshal(enriched)
	if err != nil {
		return err
	}

	if payloadContent != "" && payloadContent != lt.PayloadContent {
		if err := h.store.UpdateLoadTestPayloadContent(ctx, testID, payloadContent); err != nil {
			return err
		}
	}

	return h.store.UpdateLoadTestResult(ctx, testID, lt.Status, resultJSON)
}
