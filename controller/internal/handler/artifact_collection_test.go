package handler

import (
	"testing"
	"time"

	"github.com/shiva-load-testing/controller/internal/completion"
	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
)

func TestClassifyArtifactCollectionComplete(t *testing.T) {
	collection := classifyArtifactCollection(
		[]string{"worker1", "worker2"},
		&scriptgen.MergedSummaryMetrics{
			RawWorkerCount: 2,
			Workers: []scriptgen.WorkerSummaryMetrics{
				{Name: "worker1"},
				{Name: "worker2"},
			},
		},
	)

	if collection.Status != "complete" {
		t.Fatalf("expected complete status, got %q", collection.Status)
	}
	if collection.ExpectedWorkerCount != 2 || collection.ReceivedWorkerSummaryCount != 2 {
		t.Fatalf("expected 2/2 workers, got %d/%d", collection.ReceivedWorkerSummaryCount, collection.ExpectedWorkerCount)
	}
	if len(collection.MissingWorkers) != 0 {
		t.Fatalf("expected no missing workers, got %#v", collection.MissingWorkers)
	}
}

func TestClassifyArtifactCollectionPartial(t *testing.T) {
	collection := classifyArtifactCollection(
		[]string{"worker1", "worker2", "worker3"},
		&scriptgen.MergedSummaryMetrics{
			RawWorkerCount: 2,
			Workers: []scriptgen.WorkerSummaryMetrics{
				{Name: "worker1"},
				{Name: "worker2"},
			},
		},
	)

	if collection.Status != "partial" {
		t.Fatalf("expected partial status, got %q", collection.Status)
	}
	if collection.ExpectedWorkerCount != 3 || collection.ReceivedWorkerSummaryCount != 2 {
		t.Fatalf("expected 2/3 workers, got %d/%d", collection.ReceivedWorkerSummaryCount, collection.ExpectedWorkerCount)
	}
	if len(collection.MissingWorkers) != 1 || collection.MissingWorkers[0] != "worker3" {
		t.Fatalf("expected worker3 to be missing, got %#v", collection.MissingWorkers)
	}
}

func TestClassifyArtifactCollectionMissing(t *testing.T) {
	collection := classifyArtifactCollection([]string{"worker1", "worker2"}, nil)

	if collection.Status != "missing" {
		t.Fatalf("expected missing status, got %q", collection.Status)
	}
	if collection.ExpectedWorkerCount != 2 || collection.ReceivedWorkerSummaryCount != 0 {
		t.Fatalf("expected 0/2 workers, got %d/%d", collection.ReceivedWorkerSummaryCount, collection.ExpectedWorkerCount)
	}
	if len(collection.MissingWorkers) != 2 {
		t.Fatalf("expected all workers missing, got %#v", collection.MissingWorkers)
	}
}

func TestArtifactCollectionGraceWindow(t *testing.T) {
	tests := []struct {
		name     string
		metadata *model.TestMetadata
		want     time.Duration
	}{
		{
			name:     "nil metadata uses minimum",
			metadata: nil,
			want:     10 * time.Second,
		},
		{
			name: "short run still uses minimum",
			metadata: &model.TestMetadata{
				DurationS:   20,
				WorkerCount: 2,
			},
			want: 10 * time.Second,
		},
		{
			name: "medium run adds worker bonus",
			metadata: &model.TestMetadata{
				DurationS:   120,
				WorkerCount: 4,
			},
			want: 22 * time.Second,
		},
		{
			name: "long run is capped",
			metadata: &model.TestMetadata{
				DurationS:   1200,
				WorkerCount: 20,
			},
			want: 90 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := artifactCollectionGraceWindow(tt.metadata); got != tt.want {
				t.Fatalf("expected grace window %s, got %s", tt.want, got)
			}
		})
	}
}

func TestArtifactCollectionDeadlines(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)

	t.Run("reserves follow-up time when window allows", func(t *testing.T) {
		initial, final := artifactCollectionDeadlines(now, 20*time.Second, 5*time.Second, true)

		if got, want := initial, now.Add(15*time.Second); !got.Equal(want) {
			t.Fatalf("expected initial deadline %s, got %s", want, got)
		}
		if got, want := final, now.Add(20*time.Second); !got.Equal(want) {
			t.Fatalf("expected final deadline %s, got %s", want, got)
		}
	})

	t.Run("does not reserve when window is too short", func(t *testing.T) {
		initial, final := artifactCollectionDeadlines(now, 4*time.Second, 5*time.Second, true)

		if got, want := initial, now.Add(4*time.Second); !got.Equal(want) {
			t.Fatalf("expected initial deadline %s, got %s", want, got)
		}
		if got, want := final, now.Add(4*time.Second); !got.Equal(want) {
			t.Fatalf("expected final deadline %s, got %s", want, got)
		}
	})

	t.Run("keeps single deadline when follow-up reserve disabled", func(t *testing.T) {
		initial, final := artifactCollectionDeadlines(now, 20*time.Second, 5*time.Second, false)

		if got, want := initial, now.Add(20*time.Second); !got.Equal(want) {
			t.Fatalf("expected initial deadline %s, got %s", want, got)
		}
		if got, want := final, now.Add(20*time.Second); !got.Equal(want) {
			t.Fatalf("expected final deadline %s, got %s", want, got)
		}
	})
}

func TestSummaryCollectionFromRawAcceptsZeroDurationWhenMetricsExist(t *testing.T) {
	finalMetrics := &model.AggregatedMetrics{}
	raw := `--- worker1 ---
{"metrics":{"http_req_duration":{"type":"trend","contains":"time","values":{"avg":10,"med":9,"p(90)":11,"p(95)":12,"p(99)":13,"min":1,"max":20}},"http_reqs":{"type":"counter","contains":"default","values":{"count":100}}},"state":{"testRunDurationMs":0}}

--- worker2 ---
{"metrics":{"http_req_duration":{"type":"trend","contains":"time","values":{"avg":20,"med":19,"p(90)":21,"p(95)":22,"p(99)":23,"min":2,"max":30}},"http_reqs":{"type":"counter","contains":"default","values":{"count":200}}},"state":{"testRunDurationMs":0}}`

	result, err := summaryCollectionFromRaw([]string{"worker1", "worker2"}, raw, finalMetrics)
	if err != nil {
		t.Fatalf("expected zero-duration summaries with metrics to be accepted, got error: %v", err)
	}
	if !result.Loaded {
		t.Fatalf("expected summary collection to load")
	}
	if result.ArtifactCollection == nil || result.ArtifactCollection.Status != "complete" {
		t.Fatalf("expected complete artifact collection, got %#v", result.ArtifactCollection)
	}
	if finalMetrics.P99Latency <= 0 {
		t.Fatalf("expected summary percentiles to be applied to final metrics")
	}
}

func TestLoadUploadedSummaryUsesProvidedExpectedWorkers(t *testing.T) {
	const (
		testID = "test-uploaded-summary-expected-workers"
		token  = "upload-token"
	)

	registry := completion.NewRegistry()
	// Registry knows only 2 workers for this run.
	registry.RegisterRun(testID, []string{"worker1", "worker2"}, token)

	workerSummary := `{"metrics":{"http_req_duration":{"type":"trend","contains":"time","values":{"avg":10,"med":9,"p(90)":11,"p(95)":12,"p(99)":13,"min":1,"max":20}},"http_reqs":{"type":"counter","contains":"default","values":{"count":100}}},"state":{"testRunDurationMs":1000}}`
	if err := registry.StoreArtifact(testID, "worker1", token, completion.ArtifactSummary, "application/json", []byte(workerSummary)); err != nil {
		t.Fatalf("store worker1 summary: %v", err)
	}
	if err := registry.StoreArtifact(testID, "worker2", token, completion.ArtifactSummary, "application/json", []byte(workerSummary)); err != nil {
		t.Fatalf("store worker2 summary: %v", err)
	}

	handler := &TestHandler{completionRegistry: registry}
	result := handler.loadUploadedSummary(testID, &model.AggregatedMetrics{}, []string{"worker1", "worker2", "worker3"})

	if !result.Loaded {
		t.Fatalf("expected uploaded summary to be loaded")
	}
	if result.ArtifactCollection == nil {
		t.Fatalf("expected artifact collection metadata")
	}
	if result.ArtifactCollection.ExpectedWorkerCount != 3 {
		t.Fatalf("expected worker count to follow provided expected set (3), got %d", result.ArtifactCollection.ExpectedWorkerCount)
	}
	if result.ArtifactCollection.ReceivedWorkerSummaryCount != 2 {
		t.Fatalf("expected received worker count 2, got %d", result.ArtifactCollection.ReceivedWorkerSummaryCount)
	}
	if result.ArtifactCollection.Status != "partial" {
		t.Fatalf("expected partial status, got %q", result.ArtifactCollection.Status)
	}
	if len(result.ArtifactCollection.MissingWorkers) != 1 || result.ArtifactCollection.MissingWorkers[0] != "worker3" {
		t.Fatalf("expected worker3 to remain missing, got %#v", result.ArtifactCollection.MissingWorkers)
	}
}
