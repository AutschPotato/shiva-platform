package handler

import (
	"strings"
	"testing"

	"github.com/shiva-load-testing/controller/internal/completion"
	"github.com/shiva-load-testing/controller/internal/model"
)

func TestMergeResultArtifactsAppliesLateSummaryArtifacts(t *testing.T) {
	result := model.TestResult{
		Status: "completed",
		Metrics: &model.AggregatedMetrics{
			P95Latency: 123,
			P99Latency: 0,
		},
		Metadata: &model.TestMetadata{
			ArtifactCollection: &model.ArtifactCollectionMetadata{
				Status:              "missing",
				ExpectedWorkerCount: 2,
				MissingWorkers:      []string{"worker1", "worker2"},
			},
		},
	}

	snapshot := completion.Snapshot{
		TestID:          "run-1",
		ExpectedWorkers: []string{"worker1", "worker2"},
		Artifacts: map[completion.ArtifactType]map[string]completion.Artifact{
			completion.ArtifactSummary: {
				"worker1": {WorkerID: "worker1", Content: `{"metrics":{"http_req_duration":{"type":"trend","contains":"time","values":{"avg":10,"med":9,"p(90)":11,"p(95)":12,"p(99)":13,"min":1,"max":20}},"http_reqs":{"type":"counter","contains":"default","values":{"count":100}}},"state":{"testRunDurationMs":1000}}`},
				"worker2": {WorkerID: "worker2", Content: `{"metrics":{"http_req_duration":{"type":"trend","contains":"time","values":{"avg":20,"med":19,"p(90)":21,"p(95)":22,"p(99)":23,"min":2,"max":30}},"http_reqs":{"type":"counter","contains":"default","values":{"count":200}}},"state":{"testRunDurationMs":1000}}`},
			},
		},
	}

	enriched, _, changed := mergeResultArtifacts(result, snapshot, "")
	if !changed {
		t.Fatalf("expected late summary artifacts to enrich the result")
	}
	if enriched.Metadata == nil || enriched.Metadata.ArtifactCollection == nil {
		t.Fatalf("expected artifact collection metadata")
	}
	if enriched.Metadata.ArtifactCollection.Status == "missing" {
		t.Fatalf("expected artifact collection to improve beyond missing, got %q", enriched.Metadata.ArtifactCollection.Status)
	}
	if !strings.Contains(enriched.SummaryContent, "--- worker1 ---") {
		t.Fatalf("expected merged summary content to include worker markers")
	}
	if enriched.Metrics == nil || enriched.Metrics.P99Latency <= 0 {
		t.Fatalf("expected summary percentiles to enrich the metrics")
	}
}

func TestMergeRegistryArtifactsIntoResultUsesLatestRegistrySnapshot(t *testing.T) {
	registry := completion.NewRegistry()
	registry.RegisterRun("run-2", []string{"worker1", "worker2"}, "token-2")
	if err := registry.StoreArtifact("run-2", "worker1", "token-2", completion.ArtifactSummary, "application/json", []byte(`{"metrics":{"http_req_duration":{"type":"trend","contains":"time","values":{"avg":10,"med":9,"p(90)":11,"p(95)":12,"p(99)":13,"min":1,"max":20}},"http_reqs":{"type":"counter","contains":"default","values":{"count":100}}},"state":{"testRunDurationMs":1000}}`)); err != nil {
		t.Fatalf("store worker1 summary: %v", err)
	}
	if err := registry.StoreArtifact("run-2", "worker2", "token-2", completion.ArtifactSummary, "application/json", []byte(`{"metrics":{"http_req_duration":{"type":"trend","contains":"time","values":{"avg":20,"med":19,"p(90)":21,"p(95)":22,"p(99)":23,"min":2,"max":30}},"http_reqs":{"type":"counter","contains":"default","values":{"count":200}}},"state":{"testRunDurationMs":1000}}`)); err != nil {
		t.Fatalf("store worker2 summary: %v", err)
	}

	result := model.TestResult{
		Status: "completed",
		Metrics: &model.AggregatedMetrics{
			P95Latency: 123,
		},
		Metadata: &model.TestMetadata{
			ArtifactCollection: &model.ArtifactCollectionMetadata{
				Status:              "missing",
				ExpectedWorkerCount: 2,
				MissingWorkers:      []string{"worker1", "worker2"},
			},
		},
	}

	enriched, _, changed := mergeRegistryArtifactsIntoResult(result, registry, "run-2", "")
	if !changed {
		t.Fatalf("expected registry snapshot to enrich the completed result")
	}
	if enriched.Metadata == nil || enriched.Metadata.ArtifactCollection == nil {
		t.Fatalf("expected artifact collection metadata")
	}
	if enriched.Metadata.ArtifactCollection.Status == "missing" {
		t.Fatalf("expected artifact collection to improve beyond missing, got %q", enriched.Metadata.ArtifactCollection.Status)
	}
	if !strings.Contains(enriched.SummaryContent, "--- worker2 ---") {
		t.Fatalf("expected summary content from the latest registry snapshot")
	}
}
