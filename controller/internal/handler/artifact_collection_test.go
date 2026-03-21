package handler

import (
	"testing"

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
