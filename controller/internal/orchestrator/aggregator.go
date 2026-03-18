package orchestrator

import (
	"math"

	"github.com/shiva-load-testing/controller/internal/model"
)

func validLatencyValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// Aggregate merges metrics from multiple workers into a single consolidated result.
func Aggregate(workerResults []WorkerResult) *model.AggregatedMetrics {
	agg := &model.AggregatedMetrics{
		Workers: make([]model.WorkerMetrics, 0, len(workerResults)),
	}

	var totalWeight float64
	var weightedAvg, weightedMed, weightedP90, weightedP95, weightedP99 float64
	var globalMin, globalMax float64
	minSet := false

	// Track thresholds: metric name → tainted (true = breached)
	thresholds := make(map[string]bool)

	for _, wr := range workerResults {
		wm := model.WorkerMetrics{
			Address: wr.Address,
			Status:  "ok",
		}

		if wr.Error != nil {
			wm.Status = "error"
			wm.Error = wr.Error.Error()
			agg.Workers = append(agg.Workers, wm)
			continue
		}

		metrics := wr.Metrics

		// Collect threshold info from all metrics
		for name, m := range metrics {
			if m.Tainted != nil {
				// If any worker reports tainted, mark as tainted
				if existing, ok := thresholds[name]; ok {
					thresholds[name] = existing || *m.Tainted
				} else {
					thresholds[name] = *m.Tainted
				}
			}
		}

		// VUs
		if vus, ok := metrics["vus"]; ok {
			if v, found := vus.Val("value"); found {
				wm.VUs = int(v)
				agg.TotalVUs += int(v)
			}
		}

		// Requests
		var reqCount float64
		if reqs, ok := metrics["http_reqs"]; ok {
			if c, found := reqs.Val("count"); found {
				reqCount = c
				wm.Requests = c
				agg.TotalRequests += c
			}
			if r, found := reqs.Val("rate"); found {
				agg.RPS += r
			}
		}
		if businessReqs, ok := metrics["business_http_requests_total"]; ok {
			if c, found := businessReqs.Val("count"); found {
				agg.BusinessRequests += c
			}
		}

		// Iterations
		if iters, ok := metrics["iterations"]; ok {
			if c, found := iters.Val("count"); found {
				agg.Iterations += c
			}
		}

		// Data received/sent
		if dr, ok := metrics["data_received"]; ok {
			if c, found := dr.Val("count"); found {
				agg.DataReceived += c
			}
		}
		if ds, ok := metrics["data_sent"]; ok {
			if c, found := ds.Val("count"); found {
				agg.DataSent += c
			}
		}

		// HTTP failures — NOTE: k6 http_req_failed is a rate metric.
		// "passes" = requests where "request failed" is TRUE = actual failures
		// "fails"  = requests where "request failed" is FALSE = actual successes
		if hf, ok := metrics["http_req_failed"]; ok {
			if passes, found := hf.Val("passes"); found {
				agg.HTTPFailures += passes
			}
			if fails, found := hf.Val("fails"); found {
				agg.HTTPSuccesses += fails
			}
			// If passes/fails not available (k6 REST API only exposes "rate"
			// for Rate metrics during live polling), derive from rate + request count.
			if agg.HTTPFailures == 0 && agg.HTTPSuccesses == 0 && reqCount > 0 {
				if rate, found := hf.Val("rate"); found {
					agg.HTTPFailures += math.Round(reqCount * rate)
					agg.HTTPSuccesses += math.Round(reqCount * (1 - rate))
				}
			}
		}

		// HTTP status code counters (injected by InjectStatusCounters)
		if s4, ok := metrics["status_4xx"]; ok {
			if c, found := s4.Val("count"); found {
				agg.Status4xx += c
			}
		}
		if s5, ok := metrics["status_5xx"]; ok {
			if c, found := s5.Val("count"); found {
				agg.Status5xx += c
			}
		}

		// Latency (weighted by request count)
		if dur, ok := metrics["http_req_duration"]; ok {
			weight := reqCount
			if weight <= 0 {
				weight = 1
			}

			hasWeightedLatency := false

			if avg, found := dur.Val("avg"); found && validLatencyValue(avg) {
				wm.AvgLatency = avg
				weightedAvg += avg * weight
				hasWeightedLatency = true
			}
			if med, found := dur.Val("med"); found && validLatencyValue(med) {
				weightedMed += med * weight
				hasWeightedLatency = true
			}
			if p90, found := dur.Val("p(90)"); found && validLatencyValue(p90) {
				weightedP90 += p90 * weight
				hasWeightedLatency = true
			}
			if p95, found := dur.Val("p(95)"); found && validLatencyValue(p95) {
				weightedP95 += p95 * weight
				hasWeightedLatency = true
			}
			if p99, found := dur.Val("p(99)"); found && validLatencyValue(p99) {
				weightedP99 += p99 * weight
				hasWeightedLatency = true
			}
			if hasWeightedLatency {
				totalWeight += weight
			}
			if mn, found := dur.Val("min"); found && validLatencyValue(mn) {
				if !minSet || mn < globalMin {
					globalMin = mn
					minSet = true
				}
			}
			if mx, found := dur.Val("max"); found && validLatencyValue(mx) {
				if mx > globalMax {
					globalMax = mx
				}
			}
		}

		agg.Workers = append(agg.Workers, wm)
	}

	// Weighted averages
	if totalWeight > 0 {
		agg.AvgLatency = weightedAvg / totalWeight
		agg.MedLatency = weightedMed / totalWeight
		agg.P90Latency = weightedP90 / totalWeight
		agg.P95Latency = weightedP95 / totalWeight
		agg.P99Latency = weightedP99 / totalWeight
		// Note: k6 REST API does not provide p(99) by default.
		// P99 remains 0 during live polling. Real p(99) is captured from
		// handleSummary output after test completion and merged into final results.
	}
	agg.MinLatency = globalMin
	agg.MaxLatency = globalMax

	// Error/success rates from http_req_failed (most accurate)
	totalHTTP := agg.HTTPSuccesses + agg.HTTPFailures
	if totalHTTP > 0 {
		// Note: in k6, http_req_failed "passes" = requests that FAILED the check,
		// "fails" = requests that did NOT fail (i.e., succeeded).
		// This is counterintuitive: passes means "the assertion 'request failed' passed"
		// So: HTTPFailures (passes of http_req_failed) = actual failed requests
		//     HTTPSuccesses (fails of http_req_failed) = actual successful requests
		agg.ErrorRate = agg.HTTPFailures / totalHTTP
		agg.SuccessRate = agg.HTTPSuccesses / totalHTTP
	} else {
		// Fallback: use checks metric
		var totalPasses, totalFails float64
		var checksRateSum float64
		var checksRateCount int
		for _, wr := range workerResults {
			if wr.Error != nil {
				continue
			}
			if checks, ok := wr.Metrics["checks"]; ok {
				if p, found := checks.Val("passes"); found {
					totalPasses += p
				}
				if f, found := checks.Val("fails"); found {
					totalFails += f
				}
				// k6 REST API only exposes "rate" for Rate metrics during live polling
				if rate, found := checks.Val("rate"); found {
					checksRateSum += rate
					checksRateCount++
				}
			}
		}
		totalChecks := totalPasses + totalFails
		if totalChecks > 0 {
			agg.SuccessRate = totalPasses / totalChecks
			agg.ErrorRate = totalFails / totalChecks
		} else if checksRateCount > 0 {
			// Use averaged check pass rate from live polling
			agg.SuccessRate = checksRateSum / float64(checksRateCount)
			agg.ErrorRate = 1 - agg.SuccessRate
		} else if agg.TotalRequests > 0 {
			// Requests exist but no success/failure data at all — assume all failed
			// (transport-level errors produce no HTTP metrics).
			agg.SuccessRate = 0
			agg.ErrorRate = 1.0
		}
	}

	// Derive HTTPSuccesses/HTTPFailures from TotalRequests + ErrorRate
	// if they weren't populated from the k6 REST API (which only provides
	// passes/fails in handleSummary, not in the live metrics endpoint).
	if agg.HTTPSuccesses == 0 && agg.HTTPFailures == 0 && agg.TotalRequests > 0 {
		agg.HTTPFailures = math.Round(agg.TotalRequests * agg.ErrorRate)
		agg.HTTPSuccesses = agg.TotalRequests - agg.HTTPFailures
	}

	// Build threshold results
	for metric, tainted := range thresholds {
		agg.Thresholds = append(agg.Thresholds, model.ThresholdResult{
			Metric: metric,
			Passed: !tainted,
		})
	}

	return agg
}
