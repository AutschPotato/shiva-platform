package orchestrator

import (
	"testing"

	"github.com/shiva-load-testing/controller/internal/model"
)

func metric(values map[string]float64) model.K6Metric {
	return model.K6Metric{Values: values}
}

func TestAggregateIgnoresNegativeLatencySentinels(t *testing.T) {
	agg := Aggregate([]WorkerResult{
		{
			Address: "worker-a",
			Metrics: map[string]model.K6Metric{
				"http_reqs":         metric(map[string]float64{"count": 100}),
				"http_req_duration": metric(map[string]float64{"avg": 20, "med": 18, "p(90)": 30, "p(95)": 35, "p(99)": 40, "min": 10, "max": 50}),
			},
		},
		{
			Address: "worker-b",
			Metrics: map[string]model.K6Metric{
				"http_reqs":         metric(map[string]float64{"count": 0}),
				"http_req_duration": metric(map[string]float64{"avg": -1, "med": -1, "p(90)": -1, "p(95)": -1, "p(99)": -1, "min": -1, "max": -1}),
			},
		},
	})

	if agg.AvgLatency != 20 {
		t.Fatalf("expected avg latency 20, got %v", agg.AvgLatency)
	}
	if agg.MedLatency != 18 {
		t.Fatalf("expected median latency 18, got %v", agg.MedLatency)
	}
	if agg.P90Latency != 30 {
		t.Fatalf("expected p90 latency 30, got %v", agg.P90Latency)
	}
	if agg.P95Latency != 35 {
		t.Fatalf("expected p95 latency 35, got %v", agg.P95Latency)
	}
	if agg.P99Latency != 40 {
		t.Fatalf("expected p99 latency 40, got %v", agg.P99Latency)
	}
	if agg.MinLatency != 10 {
		t.Fatalf("expected min latency 10, got %v", agg.MinLatency)
	}
	if agg.MaxLatency != 50 {
		t.Fatalf("expected max latency 50, got %v", agg.MaxLatency)
	}
}

func TestAggregateCapturesBusinessRequestCounters(t *testing.T) {
	agg := Aggregate([]WorkerResult{
		{
			Address: "worker-a",
			Metrics: map[string]model.K6Metric{
				"http_reqs":                    metric(map[string]float64{"count": 100, "rate": 10}),
				"business_http_requests_total": metric(map[string]float64{"count": 96}),
			},
		},
		{
			Address: "worker-b",
			Metrics: map[string]model.K6Metric{
				"http_reqs":                    metric(map[string]float64{"count": 25, "rate": 2.5}),
				"business_http_requests_total": metric(map[string]float64{"count": 20}),
			},
		},
	})

	if agg.TotalRequests != 125 {
		t.Fatalf("expected total requests 125, got %v", agg.TotalRequests)
	}
	if agg.BusinessRequests != 116 {
		t.Fatalf("expected business requests 116, got %v", agg.BusinessRequests)
	}
	if agg.RPS != 12.5 {
		t.Fatalf("expected aggregated rps 12.5, got %v", agg.RPS)
	}
}
