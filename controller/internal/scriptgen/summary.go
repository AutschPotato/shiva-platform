package scriptgen

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shiva-load-testing/controller/internal/model"
)

// K6SummaryMetric represents a single metric from the k6 handleSummary output.
type K6SummaryMetric struct {
	Type       string                        `json:"type"`
	Contains   string                        `json:"contains"`
	Values     map[string]float64            `json:"values"`
	Thresholds map[string]k6SummaryThreshold `json:"thresholds,omitempty"`
}

type k6SummaryThreshold struct {
	OK bool `json:"ok"`
}

// K6SummaryData represents the parsed k6 handleSummary JSON output.
type K6SummaryData struct {
	Metrics map[string]K6SummaryMetric `json:"metrics"`
	State   struct {
		TestRunDurationMs float64 `json:"testRunDurationMs"`
	} `json:"state"`
}

// SummaryPercentiles holds the percentile values extracted from k6 summaries.
type SummaryPercentiles struct {
	P90 float64
	P95 float64
	P99 float64
}

type SummaryLatencyStats struct {
	Avg float64
	Med float64
	P90 float64
	P95 float64
	P99 float64
	Min float64
	Max float64
}

type SummaryBreakdownStats struct {
	Blocked        SummaryLatencyStats
	Waiting        SummaryLatencyStats
	Sending        SummaryLatencyStats
	Receiving      SummaryLatencyStats
	Connecting     SummaryLatencyStats
	TLSHandshaking SummaryLatencyStats
}

type WorkerSummaryMetrics struct {
	Name                      string
	Requests                  float64
	BusinessRequests          float64
	BusinessSuccesses         float64
	BusinessFailures          float64
	BusinessStatus2xx         float64
	BusinessStatus4xx         float64
	BusinessStatus5xx         float64
	BusinessTransportFailures float64
	AuthRequests              float64
	ErrorRate                 float64
	Latency                   SummaryLatencyStats
	BusinessLatency           SummaryLatencyStats
	DurationMs                float64
}

type MergedSummaryMetrics struct {
	RawWorkerCount            int
	HasBusinessMetrics        bool
	TotalRequests             float64
	TotalSuccesses            float64
	TotalFailures             float64
	TotalStatus4xx            float64
	TotalStatus5xx            float64
	Iterations                float64
	ChecksPasses              float64
	ChecksFails               float64
	TotalLatency              SummaryLatencyStats
	BusinessRequests          float64
	BusinessSuccesses         float64
	BusinessFailures          float64
	BusinessStatus2xx         float64
	BusinessStatus4xx         float64
	BusinessStatus5xx         float64
	BusinessTransportFailures float64
	BusinessLatency           SummaryLatencyStats
	BusinessBreakdown         *SummaryBreakdownStats
	Workers                   []WorkerSummaryMetrics
	Thresholds                []model.ThresholdResult
}

type AuthSummaryData struct {
	Status             string                   `json:"status,omitempty"`
	Message            string                   `json:"message,omitempty"`
	Mode               string                   `json:"mode"`
	TokenURL           string                   `json:"token_url"`
	ClientAuthMethod   string                   `json:"client_auth_method"`
	RefreshSkewSeconds int                      `json:"refresh_skew_seconds"`
	Metrics            model.AuthRuntimeMetrics `json:"metrics"`
}

type namedAuthSummary struct {
	Name    string
	Summary AuthSummaryData
}

type namedSummary struct {
	Name    string
	Summary K6SummaryData
}

func metricValue(metric K6SummaryMetric, key string) float64 {
	if metric.Values == nil {
		return 0
	}
	return metric.Values[key]
}

func appendUniqueStatusCodes(target []int, values []int) []int {
	if len(values) == 0 {
		return target
	}
	seen := make(map[int]struct{}, len(target))
	for _, code := range target {
		seen[code] = struct{}{}
	}
	for _, code := range values {
		if code <= 0 {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		target = append(target, code)
		seen[code] = struct{}{}
	}
	sort.Ints(target)
	return target
}

func max0(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func normalizeRoundedCount(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return math.Round(value)
}

func mergeStatusCodeCounts(target []model.StatusCodeCount, values []model.StatusCodeCount) []model.StatusCodeCount {
	if len(values) == 0 {
		return target
	}
	merged := make(map[int]float64, len(target)+len(values))
	for _, entry := range target {
		if entry.Code <= 0 || entry.Count <= 0 {
			continue
		}
		merged[entry.Code] += entry.Count
	}
	for _, entry := range values {
		if entry.Code <= 0 || entry.Count <= 0 {
			continue
		}
		merged[entry.Code] += entry.Count
	}
	codes := make([]int, 0, len(merged))
	for code := range merged {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	result := make([]model.StatusCodeCount, 0, len(codes))
	for _, code := range codes {
		result = append(result, model.StatusCodeCount{Code: code, Count: merged[code]})
	}
	return result
}

func shouldUseLatencyMin(metricName string, min float64) bool {
	if min < 0 {
		return false
	}
	switch metricName {
	case "http_req_duration", "business_http_duration_ms":
		return min > 0
	default:
		return true
	}
}

func weightedLatency(stats *SummaryLatencyStats, metric K6SummaryMetric, weight float64) {
	stats.Avg += metricValue(metric, "avg") * weight
	stats.Med += metricValue(metric, "med") * weight
	stats.P90 += metricValue(metric, "p(90)") * weight
	stats.P95 += metricValue(metric, "p(95)") * weight
	stats.P99 += metricValue(metric, "p(99)") * weight
}

func finalizeWeightedLatency(stats SummaryLatencyStats, weight float64, min float64, max float64, minSet bool) SummaryLatencyStats {
	if weight > 0 {
		stats.Avg /= weight
		stats.Med /= weight
		stats.P90 /= weight
		stats.P95 /= weight
		stats.P99 /= weight
	}
	if minSet {
		stats.Min = min
	}
	stats.Max = max
	return stats
}

func aggregateLatencyMetric(summaries []namedSummary, metricName string, weightMetricName string) (SummaryLatencyStats, bool) {
	var (
		weighted SummaryLatencyStats
		totalW   float64
		minV     float64
		maxV     float64
		minSet   bool
		foundAny bool
	)

	for _, summary := range summaries {
		metric, ok := summary.Summary.Metrics[metricName]
		if !ok {
			continue
		}
		foundAny = true

		weight := metricValue(summary.Summary.Metrics[weightMetricName], "count")
		if weight <= 0 {
			weight = metricValue(summary.Summary.Metrics["http_reqs"], "count")
		}
		if weight <= 0 {
			weight = 1
		}
		totalW += weight
		weightedLatency(&weighted, metric, weight)

		if min := metricValue(metric, "min"); shouldUseLatencyMin(metricName, min) && (!minSet || min < minV) {
			minV = min
			minSet = true
		}
		if max := metricValue(metric, "max"); max > maxV {
			maxV = max
		}
	}

	if !foundAny {
		return SummaryLatencyStats{}, false
	}
	return finalizeWeightedLatency(weighted, totalW, minV, maxV, minSet), true
}

func readSummaryFiles(outputDir string, pattern string) ([]namedSummary, error) {
	files, err := filepath.Glob(filepath.Join(outputDir, pattern))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no summary files found in %s", outputDir)
	}

	summaries := make([]namedSummary, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var summary K6SummaryData
		if err := json.Unmarshal(data, &summary); err != nil {
			continue
		}
		base := filepath.Base(f)
		name := strings.TrimPrefix(strings.TrimSuffix(base, ".json"), "summary-")
		summaries = append(summaries, namedSummary{Name: name, Summary: summary})
	}
	if len(summaries) == 0 {
		return nil, fmt.Errorf("no valid summary files found in %s", outputDir)
	}
	return summaries, nil
}

func mergeThresholds(summaries []namedSummary) []model.ThresholdResult {
	merged := make(map[string]bool)
	for _, summary := range summaries {
		for metricName, metric := range summary.Summary.Metrics {
			for _, threshold := range metric.Thresholds {
				if existing, ok := merged[metricName]; ok {
					merged[metricName] = existing && threshold.OK
				} else {
					merged[metricName] = threshold.OK
				}
			}
		}
	}

	results := make([]model.ThresholdResult, 0, len(merged))
	for metric, passed := range merged {
		results = append(results, model.ThresholdResult{Metric: metric, Passed: passed})
	}
	return results
}

func buildWorkerSummaryMetrics(summary namedSummary) WorkerSummaryMetrics {
	httpReqs := metricValue(summary.Summary.Metrics["http_reqs"], "count")
	failedRate := metricValue(summary.Summary.Metrics["http_req_failed"], "rate")
	latency, _ := aggregateLatencyMetric([]namedSummary{summary}, "http_req_duration", "http_reqs")
	businessLatency, _ := aggregateLatencyMetric([]namedSummary{summary}, "business_http_duration_ms", "business_http_requests_total")

	return WorkerSummaryMetrics{
		Name:                      summary.Name,
		Requests:                  httpReqs,
		BusinessRequests:          metricValue(summary.Summary.Metrics["business_http_requests_total"], "count"),
		BusinessSuccesses:         metricValue(summary.Summary.Metrics["business_http_success_total"], "count"),
		BusinessFailures:          metricValue(summary.Summary.Metrics["business_http_failure_total"], "count"),
		BusinessStatus2xx:         metricValue(summary.Summary.Metrics["business_status_2xx"], "count"),
		BusinessStatus4xx:         metricValue(summary.Summary.Metrics["business_status_4xx"], "count"),
		BusinessStatus5xx:         metricValue(summary.Summary.Metrics["business_status_5xx"], "count"),
		BusinessTransportFailures: metricValue(summary.Summary.Metrics["business_transport_failures_total"], "count"),
		AuthRequests:              metricValue(summary.Summary.Metrics["auth_token_requests_total"], "count"),
		ErrorRate:                 failedRate,
		Latency:                   latency,
		BusinessLatency:           businessLatency,
		DurationMs:                summary.Summary.State.TestRunDurationMs,
	}
}

func mergeSummaryMetrics(summaries []namedSummary) (*MergedSummaryMetrics, error) {
	if len(summaries) == 0 {
		return nil, fmt.Errorf("no summaries to merge")
	}

	merged := &MergedSummaryMetrics{
		RawWorkerCount: len(summaries),
		Workers:        make([]WorkerSummaryMetrics, 0, len(summaries)),
		Thresholds:     mergeThresholds(summaries),
	}

	for _, summary := range summaries {
		if summaryHasBusinessMetrics(summary.Summary) {
			merged.HasBusinessMetrics = true
		}
		httpReqs := metricValue(summary.Summary.Metrics["http_reqs"], "count")
		failedRate := metricValue(summary.Summary.Metrics["http_req_failed"], "rate")
		httpFailures := normalizeRoundedCount(httpReqs * failedRate)
		httpSuccesses := max0(httpReqs - httpFailures)

		merged.TotalRequests += httpReqs
		merged.TotalFailures += httpFailures
		merged.TotalSuccesses += httpSuccesses
		merged.TotalStatus4xx += metricValue(summary.Summary.Metrics["status_4xx"], "count")
		merged.TotalStatus5xx += metricValue(summary.Summary.Metrics["status_5xx"], "count")
		merged.Iterations += metricValue(summary.Summary.Metrics["iterations"], "count")
		merged.ChecksPasses += metricValue(summary.Summary.Metrics["checks"], "passes")
		merged.ChecksFails += metricValue(summary.Summary.Metrics["checks"], "fails")
		merged.BusinessRequests += metricValue(summary.Summary.Metrics["business_http_requests_total"], "count")
		merged.BusinessSuccesses += metricValue(summary.Summary.Metrics["business_http_success_total"], "count")
		merged.BusinessFailures += metricValue(summary.Summary.Metrics["business_http_failure_total"], "count")
		merged.BusinessStatus2xx += metricValue(summary.Summary.Metrics["business_status_2xx"], "count")
		merged.BusinessStatus4xx += metricValue(summary.Summary.Metrics["business_status_4xx"], "count")
		merged.BusinessStatus5xx += metricValue(summary.Summary.Metrics["business_status_5xx"], "count")
		merged.BusinessTransportFailures += metricValue(summary.Summary.Metrics["business_transport_failures_total"], "count")
		merged.Workers = append(merged.Workers, buildWorkerSummaryMetrics(summary))
	}

	totalLatency, _ := aggregateLatencyMetric(summaries, "http_req_duration", "http_reqs")
	merged.TotalLatency = totalLatency

	if businessLatency, ok := aggregateLatencyMetric(summaries, "business_http_duration_ms", "business_http_requests_total"); ok {
		merged.BusinessLatency = businessLatency
		breakdown := &SummaryBreakdownStats{}
		if blocked, ok := aggregateLatencyMetric(summaries, "business_http_blocked_ms", "business_http_requests_total"); ok {
			breakdown.Blocked = blocked
		}
		if waiting, ok := aggregateLatencyMetric(summaries, "business_http_waiting_ms", "business_http_requests_total"); ok {
			breakdown.Waiting = waiting
		}
		if sending, ok := aggregateLatencyMetric(summaries, "business_http_sending_ms", "business_http_requests_total"); ok {
			breakdown.Sending = sending
		}
		if receiving, ok := aggregateLatencyMetric(summaries, "business_http_receiving_ms", "business_http_requests_total"); ok {
			breakdown.Receiving = receiving
		}
		if connecting, ok := aggregateLatencyMetric(summaries, "business_http_connecting_ms", "business_http_requests_total"); ok {
			breakdown.Connecting = connecting
		}
		if tls, ok := aggregateLatencyMetric(summaries, "business_http_tls_handshaking_ms", "business_http_requests_total"); ok {
			breakdown.TLSHandshaking = tls
		}
		merged.BusinessBreakdown = breakdown
	}

	return merged, nil
}

func summaryHasBusinessMetrics(summary K6SummaryData) bool {
	if len(summary.Metrics) == 0 {
		return false
	}
	keys := []string{
		"business_http_requests_total",
		"business_http_success_total",
		"business_http_failure_total",
		"business_status_2xx",
		"business_status_4xx",
		"business_status_5xx",
		"business_transport_failures_total",
		"business_http_duration_ms",
	}
	for _, key := range keys {
		if _, ok := summary.Metrics[key]; ok {
			return true
		}
	}
	return false
}

// ReadAndMergeSummaries reads all summary-*.json files from the output directory and
// returns the merged latency percentiles used by the legacy completion path.
func ReadAndMergeSummaries(outputDir string) (*SummaryPercentiles, error) {
	merged, err := ReadMergedSummaryMetrics(outputDir)
	if err != nil {
		return nil, err
	}
	return &SummaryPercentiles{
		P90: merged.TotalLatency.P90,
		P95: merged.TotalLatency.P95,
		P99: merged.TotalLatency.P99,
	}, nil
}

func ReadMergedSummaryMetrics(outputDir string) (*MergedSummaryMetrics, error) {
	summaries, err := readSummaryFiles(outputDir, "summary-*.json")
	if err != nil {
		return nil, fmt.Errorf("glob summaries: %w", err)
	}
	return mergeSummaryMetrics(summaries)
}

// ParseRawSummaryContent parses the stored raw summary artifact format.
func ParseRawSummaryContent(content string) (*MergedSummaryMetrics, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("empty raw summary content")
	}

	blocks := strings.Split(trimmed, "\n--- ")
	summaries := make([]namedSummary, 0, len(blocks))
	for idx, block := range blocks {
		if idx == 0 && !strings.HasPrefix(block, "--- ") {
			block = strings.TrimPrefix(block, "--- ")
		} else {
			block = "--- " + block
		}
		lines := strings.SplitN(block, "\n", 2)
		if len(lines) != 2 {
			continue
		}
		header := strings.TrimSpace(lines[0])
		body := strings.TrimSpace(lines[1])
		name := strings.TrimSuffix(strings.TrimPrefix(header, "--- "), " ---")
		if name == "" || body == "" {
			continue
		}
		var summary K6SummaryData
		if err := json.Unmarshal([]byte(body), &summary); err != nil {
			continue
		}
		summaries = append(summaries, namedSummary{Name: name, Summary: summary})
	}
	if len(summaries) == 0 {
		return nil, fmt.Errorf("no valid summaries in artifact")
	}
	return mergeSummaryMetrics(summaries)
}

// ReadRawSummaries reads all summary files and returns the raw JSON content
// concatenated, suitable for storing as an artifact.
func ReadRawSummaries(outputDir string) string {
	pattern := filepath.Join(outputDir, "summary-*.json")
	files, _ := filepath.Glob(pattern)
	if len(files) == 0 {
		return ""
	}

	var parts []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		base := filepath.Base(f)
		name := strings.TrimPrefix(strings.TrimSuffix(base, ".json"), "summary-")
		var raw json.RawMessage
		if json.Unmarshal(data, &raw) == nil {
			parts = append(parts, fmt.Sprintf("--- %s ---\n%s", name, string(data)))
		}
	}
	return strings.Join(parts, "\n\n")
}

// ReadPayloadArtifact reads the first payload artifact emitted by handleSummary.
func ReadPayloadArtifact(outputDir string) string {
	pattern := filepath.Join(outputDir, "payload-*.json")
	files, _ := filepath.Glob(pattern)
	if len(files) == 0 {
		return ""
	}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "" {
			continue
		}
		return string(data)
	}
	return ""
}

func ReadAndMergeAuthSummaries(outputDir string) (*AuthSummaryData, error) {
	pattern := filepath.Join(outputDir, "auth-summary-*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob auth summaries: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no auth summary files found in %s", outputDir)
	}

	summaries := make([]namedAuthSummary, 0, len(files))

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		var summary AuthSummaryData
		if err := json.Unmarshal(data, &summary); err != nil {
			continue
		}
		base := filepath.Base(f)
		name := strings.TrimPrefix(strings.TrimSuffix(base, ".json"), "auth-summary-")
		summaries = append(summaries, namedAuthSummary{Name: name, Summary: summary})
	}

	return mergeAuthSummaries(summaries)
}

func ReadRawAuthSummaries(outputDir string) string {
	pattern := filepath.Join(outputDir, "auth-summary-*.json")
	files, _ := filepath.Glob(pattern)
	if len(files) == 0 {
		return ""
	}

	var parts []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		base := filepath.Base(f)
		name := strings.TrimPrefix(strings.TrimSuffix(base, ".json"), "auth-summary-")
		var raw json.RawMessage
		if json.Unmarshal(data, &raw) == nil {
			parts = append(parts, fmt.Sprintf("--- %s ---\n%s", name, string(data)))
		}
	}
	return strings.Join(parts, "\n\n")
}

func ParseRawAuthSummaryContent(content string) (*AuthSummaryData, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("empty raw auth summary content")
	}

	blocks := strings.Split(trimmed, "\n--- ")
	summaries := make([]namedAuthSummary, 0, len(blocks))
	for idx, block := range blocks {
		if idx == 0 && !strings.HasPrefix(block, "--- ") {
			block = strings.TrimPrefix(block, "--- ")
		} else {
			block = "--- " + block
		}
		lines := strings.SplitN(block, "\n", 2)
		if len(lines) != 2 {
			continue
		}
		header := strings.TrimSpace(lines[0])
		body := strings.TrimSpace(lines[1])
		name := strings.TrimSuffix(strings.TrimPrefix(header, "--- "), " ---")
		if name == "" || body == "" {
			continue
		}
		var summary AuthSummaryData
		if err := json.Unmarshal([]byte(body), &summary); err != nil {
			continue
		}
		summaries = append(summaries, namedAuthSummary{Name: name, Summary: summary})
	}
	return mergeAuthSummaries(summaries)
}

func mergeAuthSummaries(summaries []namedAuthSummary) (*AuthSummaryData, error) {
	if len(summaries) == 0 {
		return nil, fmt.Errorf("no valid auth summary data found")
	}

	merged := &AuthSummaryData{Status: "complete"}
	var totalWeight float64
	var weightedAvgMs float64
	var weightedP95Ms float64
	var weightedP99Ms float64

	for _, named := range summaries {
		summary := named.Summary
		if merged.Mode == "" {
			merged.Mode = summary.Mode
		}
		if merged.TokenURL == "" {
			merged.TokenURL = summary.TokenURL
		}
		if merged.ClientAuthMethod == "" {
			merged.ClientAuthMethod = summary.ClientAuthMethod
		}
		if merged.RefreshSkewSeconds == 0 {
			merged.RefreshSkewSeconds = summary.RefreshSkewSeconds
		}
		if merged.Message == "" && summary.Message != "" {
			merged.Message = summary.Message
		}
		if summary.Status == "aborted" {
			merged.Status = "aborted"
		}

		merged.Metrics.TokenRequestsTotal += summary.Metrics.TokenRequestsTotal
		merged.Metrics.TokenSuccessTotal += summary.Metrics.TokenSuccessTotal
		merged.Metrics.TokenFailureTotal += summary.Metrics.TokenFailureTotal
		merged.Metrics.TokenRefreshTotal += summary.Metrics.TokenRefreshTotal
		merged.Metrics.TokenReuseHitsTotal += summary.Metrics.TokenReuseHitsTotal
		merged.Metrics.ResponseStatusCodes = mergeStatusCodeCounts(merged.Metrics.ResponseStatusCodes, summary.Metrics.ResponseStatusCodes)
		merged.Metrics.AbortTriggered = merged.Metrics.AbortTriggered || summary.Metrics.AbortTriggered
		if merged.Metrics.AbortCause == "" && summary.Metrics.AbortCause != "" {
			merged.Metrics.AbortCause = summary.Metrics.AbortCause
		}
		if merged.Metrics.AbortReason == "" && summary.Metrics.AbortReason != "" {
			merged.Metrics.AbortReason = summary.Metrics.AbortReason
		}
		merged.Metrics.AbortRetryable = merged.Metrics.AbortRetryable || summary.Metrics.AbortRetryable
		merged.Metrics.AbortHTTPStatusCodes = appendUniqueStatusCodes(merged.Metrics.AbortHTTPStatusCodes, summary.Metrics.AbortHTTPStatusCodes)
		if summary.Metrics.TokenRequestMaxMs > merged.Metrics.TokenRequestMaxMs {
			merged.Metrics.TokenRequestMaxMs = summary.Metrics.TokenRequestMaxMs
		}

		weight := summary.Metrics.TokenRequestsTotal
		if weight <= 0 {
			continue
		}
		totalWeight += weight
		weightedAvgMs += summary.Metrics.TokenRequestAvgMs * weight
		weightedP95Ms += summary.Metrics.TokenRequestP95Ms * weight
		weightedP99Ms += summary.Metrics.TokenRequestP99Ms * weight
	}

	if merged.Metrics.TokenRequestsTotal > 0 {
		merged.Metrics.TokenSuccessRate = merged.Metrics.TokenSuccessTotal / merged.Metrics.TokenRequestsTotal
	}
	if merged.Status == "aborted" && merged.Message == "" && merged.Metrics.AbortReason != "" {
		merged.Message = merged.Metrics.AbortReason
	}
	if totalWeight > 0 {
		merged.Metrics.TokenRequestAvgMs = weightedAvgMs / totalWeight
		merged.Metrics.TokenRequestP95Ms = weightedP95Ms / totalWeight
		merged.Metrics.TokenRequestP99Ms = weightedP99Ms / totalWeight
	}
	if merged.Mode == "" && merged.Metrics.TokenRequestsTotal == 0 && merged.Metrics.TokenFailureTotal == 0 {
		return nil, fmt.Errorf("no valid auth summary data found")
	}
	return merged, nil
}

// CleanupSummaries removes all summary-*.json files from the output directory.
func CleanupSummaries(outputDir string) {
	pattern := filepath.Join(outputDir, "summary-*.json")
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		_ = os.Remove(f)
	}
}

// CleanupPayloadArtifacts removes all payload-*.json files from the output directory.
func CleanupPayloadArtifacts(outputDir string) {
	pattern := filepath.Join(outputDir, "payload-*.json")
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		_ = os.Remove(f)
	}
}

func CleanupAuthSummaries(outputDir string) {
	pattern := filepath.Join(outputDir, "auth-summary-*.json")
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		_ = os.Remove(f)
	}
}
