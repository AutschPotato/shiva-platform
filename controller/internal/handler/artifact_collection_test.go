package handler

import (
	"testing"
	"time"

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
