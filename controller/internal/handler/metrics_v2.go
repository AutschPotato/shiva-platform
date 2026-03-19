package handler

import (
	"math"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
)

func safeRate(successes float64, total float64) (successRate float64, errorRate float64) {
	if total <= 0 {
		return 0, 0
	}
	successRate = successes / total
	errorRate = 1 - successRate
	return successRate, errorRate
}

func metricQuality(key string, status string, source string, scope string, reason string) model.MetricQualityFlag {
	return model.MetricQualityFlag{
		Key:                 key,
		Status:              status,
		Source:              source,
		Scope:               scope,
		ApproximationReason: reason,
	}
}

func upsertMetricQuality(flags []model.MetricQualityFlag, flag model.MetricQualityFlag) []model.MetricQualityFlag {
	for idx := range flags {
		if flags[idx].Key == flag.Key {
			flags[idx] = flag
			return flags
		}
	}
	return append(flags, flag)
}

func max0(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func sanitizedLatencyBlock(metric string, scope string, stats scriptgen.SummaryLatencyStats) model.LatencyMetricBlock {
	return model.LatencyMetricBlock{
		Metric: metric,
		Scope:  scope,
		AvgMs:  clampLatencyValue(stats.Avg),
		MedMs:  clampLatencyValue(stats.Med),
		P90Ms:  clampLatencyValue(stats.P90),
		P95Ms:  clampLatencyValue(stats.P95),
		P99Ms:  clampLatencyValue(stats.P99),
		MinMs:  clampLatencyValue(stats.Min),
		MaxMs:  clampLatencyValue(stats.Max),
	}
}

func sanitizedLegacyLatencyBlock(metric string, scope string, metrics *model.AggregatedMetrics) model.LatencyMetricBlock {
	if metrics == nil {
		return model.LatencyMetricBlock{Metric: metric, Scope: scope}
	}
	return model.LatencyMetricBlock{
		Metric: metric,
		Scope:  scope,
		AvgMs:  clampLatencyValue(metrics.AvgLatency),
		MedMs:  clampLatencyValue(metrics.MedLatency),
		P90Ms:  clampLatencyValue(metrics.P90Latency),
		P95Ms:  clampLatencyValue(metrics.P95Latency),
		P99Ms:  clampLatencyValue(metrics.P99Latency),
		MinMs:  clampLatencyValue(metrics.MinLatency),
		MaxMs:  clampLatencyValue(metrics.MaxLatency),
	}
}

func sanitizedBreakdownMetric(stats scriptgen.SummaryLatencyStats) model.BreakdownMetricBlock {
	return model.BreakdownMetricBlock{
		AvgMs: clampLatencyValue(stats.Avg),
		P95Ms: clampLatencyValue(stats.P95),
		P99Ms: clampLatencyValue(stats.P99),
		MaxMs: clampLatencyValue(stats.Max),
	}
}

func buildMetricsV2(legacy *model.AggregatedMetrics, rawSummary string, metadata *model.TestMetadata) *model.MetricsV2 {
	if legacy == nil && rawSummary == "" && metadata == nil {
		return nil
	}

	result := &model.MetricsV2{}
	quality := make([]model.MetricQualityFlag, 0, 8)

	if legacy != nil {
		result.HTTPTotal = model.HTTPMetricsBlock{
			Requests:          legacy.TotalRequests,
			RPS:               legacy.RPS,
			Successes:         legacy.HTTPSuccesses,
			Failures:          legacy.HTTPFailures,
			SuccessRate:       legacy.SuccessRate,
			ErrorRate:         legacy.ErrorRate,
			Status4xx:         legacy.Status4xx,
			Status5xx:         legacy.Status5xx,
			OtherFailures:     max0(legacy.HTTPFailures - legacy.Status4xx - legacy.Status5xx),
			DataReceivedBytes: legacy.DataReceived,
			DataSentBytes:     legacy.DataSent,
		}
		result.Iterations = model.MetricCounter{
			Count: legacy.Iterations,
			Rate:  rateFromDuration(legacy.Iterations, metadata),
		}
		result.PrimaryLatency = sanitizedLegacyLatencyBlock("http_req_duration", "http_total", legacy)
		result.Thresholds = append(result.Thresholds, legacy.Thresholds...)
		quality = upsertMetricQuality(quality, metricQuality("http_total", "legacy", "legacy_result_metrics", "global", "Final total HTTP metrics currently fall back to the stored live snapshot because no parsed summary total has been applied yet."))
	}

	var mergedSummary *scriptgen.MergedSummaryMetrics
	if rawSummary != "" {
		if parsed, err := scriptgen.ParseRawSummaryContent(rawSummary); err == nil {
			mergedSummary = parsed
		} else {
			quality = append(quality, metricQuality("summary_artifact", "unavailable", "summary_content", "global", err.Error()))
		}
	}

	if mergedSummary != nil {
		totalSuccessRate, totalErrorRate := safeRate(mergedSummary.TotalSuccesses, mergedSummary.TotalRequests)
		result.HTTPTotal = model.HTTPMetricsBlock{
			Requests:          mergedSummary.TotalRequests,
			RPS:               rateFromDuration(mergedSummary.TotalRequests, metadata),
			Successes:         mergedSummary.TotalSuccesses,
			Failures:          mergedSummary.TotalFailures,
			SuccessRate:       totalSuccessRate,
			ErrorRate:         totalErrorRate,
			Status4xx:         mergedSummary.TotalStatus4xx,
			Status5xx:         mergedSummary.TotalStatus5xx,
			OtherFailures:     max0(mergedSummary.TotalFailures - mergedSummary.TotalStatus4xx - mergedSummary.TotalStatus5xx),
			DataReceivedBytes: result.HTTPTotal.DataReceivedBytes,
			DataSentBytes:     result.HTTPTotal.DataSentBytes,
		}
		result.Iterations = model.MetricCounter{
			Count: mergedSummary.Iterations,
			Rate:  rateFromDuration(mergedSummary.Iterations, metadata),
		}
		quality = upsertMetricQuality(quality, metricQuality("http_total", "exact", "summary_http_reqs", "global", ""))
		result.Checks = buildCheckMetrics(mergedSummary)
		result.Workers = buildWorkerMetricsV2(mergedSummary, legacy)
		if len(result.Thresholds) == 0 {
			result.Thresholds = mergedSummary.Thresholds
		}

		if mergedSummary.BusinessRequests > 0 {
			successRate, errorRate := safeRate(mergedSummary.BusinessSuccesses, mergedSummary.BusinessRequests)
			result.HTTPBusiness = model.HTTPMetricsBlock{
				Requests:      mergedSummary.BusinessRequests,
				RPS:           rateFromDuration(mergedSummary.BusinessRequests, metadata),
				Successes:     mergedSummary.BusinessSuccesses,
				Failures:      mergedSummary.BusinessFailures,
				SuccessRate:   successRate,
				ErrorRate:     errorRate,
				Status2xx:     mergedSummary.BusinessStatus2xx,
				Status4xx:     mergedSummary.BusinessStatus4xx,
				Status5xx:     mergedSummary.BusinessStatus5xx,
				NetworkErrors: mergedSummary.BusinessTransportFailures,
				OtherFailures: max0(mergedSummary.BusinessFailures - mergedSummary.BusinessStatus4xx - mergedSummary.BusinessStatus5xx - mergedSummary.BusinessTransportFailures),
			}
			result.PrimaryLatency = sanitizedLatencyBlock("business_http_duration_ms", "http_business", mergedSummary.BusinessLatency)
			quality = upsertMetricQuality(quality, metricQuality("http_business", "exact", "summary_custom_business_counters", "global", ""))
			quality = upsertMetricQuality(quality, metricQuality("latency_primary", "approximate", "summary_weighted_worker_percentiles", "global", "Global percentiles are reconstructed from per-worker handleSummary trend values."))
			if mergedSummary.BusinessBreakdown != nil {
				result.LatencyBreakdown = &model.LatencyBreakdownBlock{
					Blocked:        sanitizedBreakdownMetric(mergedSummary.BusinessBreakdown.Blocked),
					Waiting:        sanitizedBreakdownMetric(mergedSummary.BusinessBreakdown.Waiting),
					Sending:        sanitizedBreakdownMetric(mergedSummary.BusinessBreakdown.Sending),
					Receiving:      sanitizedBreakdownMetric(mergedSummary.BusinessBreakdown.Receiving),
					Connecting:     sanitizedBreakdownMetric(mergedSummary.BusinessBreakdown.Connecting),
					TLSHandshaking: sanitizedBreakdownMetric(mergedSummary.BusinessBreakdown.TLSHandshaking),
				}
				quality = upsertMetricQuality(quality, metricQuality("latency_breakdown", "approximate", "summary_weighted_worker_percentiles", "global", "Latency breakdown percentiles are reconstructed from per-worker trend summaries."))
			}
		}
	}

	if mergedSummary != nil && mergedSummary.HasBusinessMetrics {
		if auxMetrics, hasAux := derivedAuxiliaryHTTPMetrics(result.HTTPTotal, result.HTTPBusiness, metadata); hasAux {
			result.HTTPAuxiliary = auxMetrics
			quality = upsertMetricQuality(quality, metricQuality("http_auxiliary", "exact", "summary_total_minus_business", "global", ""))
		}
	} else if auxMetrics, hasAux := authHTTPMetrics(metadata); hasAux {
		result.HTTPAuxiliary = auxMetrics
		quality = upsertMetricQuality(quality, metricQuality("http_auxiliary", "approximate", "auth_summary_logical_token_requests", "global", "Auxiliary HTTP traffic falls back to logical token acquisition attempts because exact non-business HTTP totals could not be derived."))
	}

	if result.HTTPBusiness.Requests == 0 && legacy != nil {
		sourceTotal := result.HTTPTotal
		if result.HTTPAuxiliary.Requests > 0 && sourceTotal.Requests > 0 {
			requests := max0(sourceTotal.Requests - result.HTTPAuxiliary.Requests)
			successes := max0(sourceTotal.Successes - result.HTTPAuxiliary.Successes)
			failures := max0(sourceTotal.Failures - result.HTTPAuxiliary.Failures)
			successRate, errorRate := safeRate(successes, requests)
			result.HTTPBusiness = model.HTTPMetricsBlock{
				Requests:      requests,
				RPS:           rateFromDuration(requests, metadata),
				Successes:     successes,
				Failures:      failures,
				SuccessRate:   successRate,
				ErrorRate:     errorRate,
				Status4xx:     max0(sourceTotal.Status4xx - result.HTTPAuxiliary.Status4xx),
				Status5xx:     max0(sourceTotal.Status5xx - result.HTTPAuxiliary.Status5xx),
				OtherFailures: max0(failures - max0(sourceTotal.Status4xx-result.HTTPAuxiliary.Status4xx) - max0(sourceTotal.Status5xx-result.HTTPAuxiliary.Status5xx)),
			}
			quality = upsertMetricQuality(quality, metricQuality("http_business", "approximate", "total_minus_auth", "global", "Business traffic was approximated by subtracting auth token requests from total HTTP requests."))
		} else {
			result.HTTPBusiness = result.HTTPTotal
			quality = upsertMetricQuality(quality, metricQuality("http_business", "legacy", "legacy_total_http", "global", "Run has no business-specific counters; total HTTP traffic is used as a legacy fallback."))
			quality = upsertMetricQuality(quality, metricQuality("latency_primary", "approximate", "legacy_total_http", "global", "Primary latency falls back to total HTTP duration because no business latency metric was captured."))
		}
	}

	if result.PrimaryLatency.Metric == "" {
		result.PrimaryLatency = sanitizedLegacyLatencyBlock("http_req_duration", "http_total", legacy)
	}

	if len(result.Workers) == 0 && legacy != nil {
		result.Workers = make([]model.WorkerMetricsV2, 0, len(legacy.Workers))
		for _, worker := range legacy.Workers {
			result.Workers = append(result.Workers, model.WorkerMetricsV2{
				Address:      worker.Address,
				Status:       worker.Status,
				Error:        worker.Error,
				Requests:     worker.Requests,
				AvgLatencyMs: clampLatencyValue(worker.AvgLatency),
			})
		}
		quality = upsertMetricQuality(quality, metricQuality("workers", "legacy", "legacy_worker_snapshot", "per_worker", "Worker drilldown falls back to the coarse live aggregate because no parsed worker summary is available."))
	} else if len(result.Workers) > 0 {
		quality = upsertMetricQuality(quality, metricQuality("workers", "approximate", "summary_worker_metrics", "per_worker", "Per-worker latency percentiles are taken from worker summaries; global worker timing windows remain controller-based."))
	}

	if mergedSummary == nil {
		result.Checks = model.CheckMetrics{}
		quality = upsertMetricQuality(quality, metricQuality("checks", "unavailable", "summary_content", "global", "No parsed handleSummary artifact was available for check aggregation."))
	}

	result.QualityFlags = quality
	return result
}

func buildCheckMetrics(summary *scriptgen.MergedSummaryMetrics) model.CheckMetrics {
	if summary == nil {
		return model.CheckMetrics{}
	}
	total := summary.ChecksPasses + summary.ChecksFails
	if total <= 0 {
		return model.CheckMetrics{}
	}
	return model.CheckMetrics{
		Passes:   summary.ChecksPasses,
		Fails:    summary.ChecksFails,
		PassRate: summary.ChecksPasses / total,
		FailRate: summary.ChecksFails / total,
	}
}

func buildWorkerMetricsV2(summary *scriptgen.MergedSummaryMetrics, legacy *model.AggregatedMetrics) []model.WorkerMetricsV2 {
	if summary == nil || len(summary.Workers) == 0 {
		return nil
	}

	workers := make([]model.WorkerMetricsV2, 0, len(summary.Workers))
	for idx, worker := range summary.Workers {
		status := "ok"
		errMsg := ""
		address := worker.Name
		if legacy != nil && idx < len(legacy.Workers) {
			if legacy.Workers[idx].Address != "" {
				address = legacy.Workers[idx].Address
			}
			if legacy.Workers[idx].Status != "" {
				status = legacy.Workers[idx].Status
			}
			errMsg = legacy.Workers[idx].Error
		}

		workers = append(workers, model.WorkerMetricsV2{
			Address:           address,
			Status:            status,
			Error:             errMsg,
			Requests:          worker.Requests,
			BusinessRequests:  worker.BusinessRequests,
			AuxiliaryRequests: selectWorkerAuxiliaryRequests(worker),
			AvgLatencyMs:      clampLatencyValue(selectWorkerLatency(worker, "avg")),
			P95LatencyMs:      clampLatencyValue(selectWorkerLatency(worker, "p95")),
			P99LatencyMs:      clampLatencyValue(selectWorkerLatency(worker, "p99")),
			ErrorRate:         math.Max(worker.ErrorRate, 0),
			ActiveDurationS:   worker.DurationMs / 1000,
		})
	}

	return workers
}

func selectWorkerAuxiliaryRequests(worker scriptgen.WorkerSummaryMetrics) float64 {
	derived := max0(worker.Requests - worker.BusinessRequests)
	if derived > 0 {
		return derived
	}
	return max0(worker.AuthRequests)
}

func selectWorkerLatency(worker scriptgen.WorkerSummaryMetrics, key string) float64 {
	source := worker.BusinessLatency
	if worker.BusinessRequests <= 0 {
		source = worker.Latency
	}

	switch key {
	case "avg":
		return source.Avg
	case "p95":
		return source.P95
	case "p99":
		return source.P99
	default:
		return source.Avg
	}
}

func authHTTPMetrics(metadata *model.TestMetadata) (model.HTTPMetricsBlock, bool) {
	if metadata == nil || metadata.Auth == nil || metadata.Auth.Metrics == nil {
		return model.HTTPMetricsBlock{}, false
	}

	auth := metadata.Auth.Metrics
	requests := auth.TokenRequestsTotal
	successRate, errorRate := safeRate(auth.TokenSuccessTotal, requests)

	return model.HTTPMetricsBlock{
		Requests:    requests,
		Successes:   auth.TokenSuccessTotal,
		Failures:    auth.TokenFailureTotal,
		SuccessRate: successRate,
		ErrorRate:   errorRate,
	}, requests > 0 || auth.TokenFailureTotal > 0
}

func derivedAuxiliaryHTTPMetrics(total model.HTTPMetricsBlock, business model.HTTPMetricsBlock, metadata *model.TestMetadata) (model.HTTPMetricsBlock, bool) {
	requests := max0(total.Requests - business.Requests)
	successes := max0(total.Successes - business.Successes)
	failures := max0(total.Failures - business.Failures)
	status4xx := max0(total.Status4xx - business.Status4xx)
	status5xx := max0(total.Status5xx - business.Status5xx)
	if requests <= 0 && successes <= 0 && failures <= 0 {
		return model.HTTPMetricsBlock{}, false
	}

	successRate, errorRate := safeRate(successes, requests)
	return model.HTTPMetricsBlock{
		Requests:      requests,
		RPS:           rateFromDuration(requests, metadata),
		Successes:     successes,
		Failures:      failures,
		SuccessRate:   successRate,
		ErrorRate:     errorRate,
		Status4xx:     status4xx,
		Status5xx:     status5xx,
		OtherFailures: max0(failures - status4xx - status5xx),
	}, true
}

func rateFromDuration(count float64, metadata *model.TestMetadata) float64 {
	if metadata == nil || metadata.DurationS <= 0 {
		return 0
	}
	return count / metadata.DurationS
}
