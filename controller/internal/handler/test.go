package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/shiva-load-testing/controller/internal/completion"
	"github.com/shiva-load-testing/controller/internal/middleware"
	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/orchestrator"
	"github.com/shiva-load-testing/controller/internal/scheduler"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
	"github.com/shiva-load-testing/controller/internal/store"
)

// pendingTestInfo holds metadata and warnings for a running test.
type pendingTestInfo struct {
	Meta         *model.TestMetadata
	Warnings     []model.ConflictWarning
	Executor     string
	Controllable bool
}

type completionData struct {
	metrics    *model.AggregatedMetrics
	timeSeries []model.TimePoint
	metadata   *model.TestMetadata
	warnings   []model.ConflictWarning
}

type summaryCollectionResult struct {
	Raw                string
	Loaded             bool
	ArtifactCollection *model.ArtifactCollectionMetadata
}

type executionPreparation struct {
	scriptContent    string
	configContent    string
	conflictWarnings []model.ConflictWarning
	configStages     []model.Stage
	envVars          map[string]string
	executorType     scriptgen.ExecutorType
	controllable     bool
	payload          *scriptgen.BuilderPayloadArtifacts
}

type scheduleCompletionNotifier interface {
	OnTestComplete(testID string, status string, errMsg string)
}

type TestHandler struct {
	store                 *store.Store
	orch                  *orchestrator.Orchestrator
	logger                *slog.Logger
	scriptsDir            string
	outputDir             string
	internalControllerURL string
	completionRegistry    *completion.Registry
	mu                    sync.Mutex
	pendingMeta           map[string]*pendingTestInfo
	scheduleNotifier      scheduleCompletionNotifier
}

func NewTestHandler(s *store.Store, orch *orchestrator.Orchestrator, logger *slog.Logger, scriptsDir, outputDir string, completionRegistry *completion.Registry, internalControllerURL string) *TestHandler {
	if completionRegistry == nil {
		completionRegistry = completion.NewRegistry()
	}
	if strings.TrimSpace(internalControllerURL) == "" {
		internalControllerURL = "http://controller:8080"
	}
	return &TestHandler{
		store:                 s,
		orch:                  orch,
		logger:                logger,
		scriptsDir:            scriptsDir,
		outputDir:             outputDir,
		internalControllerURL: internalControllerURL,
		completionRegistry:    completionRegistry,
		pendingMeta:           make(map[string]*pendingTestInfo),
	}
}

func (h *TestHandler) SetScheduleCompletionNotifier(notifier scheduleCompletionNotifier) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scheduleNotifier = notifier
}

func validateStartRequest(req *model.TestRequest) error {
	if req.ProjectName == "" {
		return fmt.Errorf("project_name is required")
	}
	return validateExecutionRequest(req)
}

func validateExecutionRequest(req *model.TestRequest) error {
	hasScript := req.ScriptContent != ""
	hasBuilder := req.URL != ""
	if !hasScript && !hasBuilder {
		return fmt.Errorf("provide either script_content or url (builder)")
	}
	if err := normalizeAuthInput(&req.Auth); err != nil {
		return err
	}
	if req.Auth.Enabled && strings.TrimSpace(req.Auth.ClientSecret) == "" {
		return fmt.Errorf("auth_client_secret is required when auth_enabled is true")
	}
	if _, err := normalizeTestRequestPayload(req); err != nil {
		return err
	}
	return nil
}

func normalizeTestRequestPayload(req *model.TestRequest) (*scriptgen.BuilderPayloadArtifacts, error) {
	return normalizePayloadFields(req.ScriptContent != "", &req.HTTPMethod, &req.ContentType, &req.PayloadJSON, &req.PayloadTargetKiB)
}

func normalizeTemplateRequestPayload(req *model.TestTemplateRequest) error {
	if err := normalizeAuthInput(&req.Auth); err != nil {
		return err
	}
	_, err := normalizePayloadFields(req.ScriptContent != "", &req.HTTPMethod, &req.ContentType, &req.PayloadJSON, &req.PayloadTargetKiB)
	return err
}

func normalizeScheduleRequestPayload(req *model.CreateScheduleRequest) error {
	if err := normalizeAuthInput(&req.Auth); err != nil {
		return err
	}
	_, err := normalizePayloadFields(req.ScriptContent != "", &req.HTTPMethod, &req.ContentType, &req.PayloadJSON, &req.PayloadTargetKiB)
	return err
}

func normalizeAuthInput(auth *model.AuthInput) error {
	auth.Mode = strings.TrimSpace(auth.Mode)
	auth.TokenURL = strings.TrimSpace(auth.TokenURL)
	auth.ClientID = strings.TrimSpace(auth.ClientID)
	auth.ClientAuthMethod = strings.TrimSpace(auth.ClientAuthMethod)

	if !auth.Enabled {
		*auth = model.AuthInput{}
		return nil
	}

	if auth.Mode == "" {
		auth.Mode = "oauth_client_credentials"
	}
	if auth.Mode != "oauth_client_credentials" {
		return fmt.Errorf("auth_mode must be oauth_client_credentials")
	}
	if auth.TokenURL == "" {
		return fmt.Errorf("auth_token_url is required when auth_enabled is true")
	}
	if parsed, err := url.Parse(auth.TokenURL); err != nil || parsed == nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("auth_token_url must be an absolute http or https URL")
	}
	if auth.ClientID == "" {
		return fmt.Errorf("auth_client_id is required when auth_enabled is true")
	}
	if auth.ClientAuthMethod == "" {
		auth.ClientAuthMethod = "basic"
	}
	if auth.ClientAuthMethod != "basic" && auth.ClientAuthMethod != "body" {
		return fmt.Errorf("auth_client_auth_method must be basic or body")
	}
	if auth.RefreshSkewSeconds <= 0 {
		auth.RefreshSkewSeconds = 30
	}

	return nil
}

func authConfigFromRuntimeInput(input model.AuthInput) model.AuthConfig {
	if !input.Enabled {
		return model.AuthConfig{}
	}

	cfg := model.AuthConfig{
		Enabled:            true,
		Mode:               input.Mode,
		TokenURL:           input.TokenURL,
		ClientID:           input.ClientID,
		ClientAuthMethod:   input.ClientAuthMethod,
		RefreshSkewSeconds: input.RefreshSkewSeconds,
	}
	if input.ClientSecret != "" {
		cfg.SecretSource = "runtime_only"
		cfg.SecretConfigured = true
	}
	return cfg
}

func authConfigFromStoredInput(input model.AuthInput, existing *model.AuthConfig) model.AuthConfig {
	if !input.Enabled {
		return model.AuthConfig{}
	}

	cfg := model.AuthConfig{
		Enabled:            true,
		Mode:               input.Mode,
		TokenURL:           input.TokenURL,
		ClientID:           input.ClientID,
		ClientAuthMethod:   input.ClientAuthMethod,
		RefreshSkewSeconds: input.RefreshSkewSeconds,
	}

	if existing != nil {
		cfg.ClientSecretEncrypted = existing.ClientSecretEncrypted
		cfg.SecretSource = existing.SecretSource
		cfg.SecretConfigured = existing.SecretConfigured
	}

	if input.ClearSecret {
		cfg.ClientSecretEncrypted = ""
		cfg.SecretSource = ""
		cfg.SecretConfigured = false
	}

	if cfg.ClientSecretEncrypted != "" {
		if cfg.SecretSource == "" {
			cfg.SecretSource = "persisted_encrypted"
		}
		cfg.SecretConfigured = true
	}

	return cfg
}

func normalizePayloadFields(hasScript bool, method *string, contentType *string, payloadJSON *string, targetKiB *int) (*scriptgen.BuilderPayloadArtifacts, error) {
	if targetKiB != nil && *targetKiB < 0 {
		return nil, fmt.Errorf("payload_target_kib must be greater than or equal to 0")
	}

	trimmedPayload := strings.TrimSpace(*payloadJSON)
	if hasScript {
		if scriptgen.HasPayloadConfiguration(*method, *contentType, trimmedPayload, *targetKiB) {
			return nil, fmt.Errorf("payload settings are only supported for builder-mode tests")
		}
		*payloadJSON = trimmedPayload
		return nil, nil
	}

	normalizedMethod := scriptgen.NormalizeHTTPMethod(*method)
	normalizedContentType := scriptgen.NormalizeContentType(*contentType)
	candidate := &model.TestRequest{
		HTTPMethod:       normalizedMethod,
		ContentType:      normalizedContentType,
		PayloadJSON:      trimmedPayload,
		PayloadTargetKiB: *targetKiB,
	}

	payloadArtifacts, err := scriptgen.BuildBuilderPayloadArtifacts(candidate)
	if err != nil {
		return nil, err
	}

	*method = normalizedMethod
	*contentType = normalizedContentType
	*payloadJSON = trimmedPayload
	return payloadArtifacts, nil
}

func checkManualStartConflict(ctx context.Context, st *store.Store, confirm bool) *model.ScheduleConflict {
	if confirm {
		return nil
	}

	// Use a 2-hour window as worst-case estimate for tests with unknown duration.
	proposed := scheduler.TimeSlot{
		Start: time.Now(),
		End:   time.Now().Add(2 * time.Hour),
	}
	conflict, _ := scheduler.CheckOverlap(ctx, st, proposed, "", nil)
	return conflict
}

func newPayloadMetadata(payload *scriptgen.BuilderPayloadArtifacts) *model.PayloadMetadata {
	if payload == nil {
		return nil
	}
	return &model.PayloadMetadata{
		HTTPMethod:  payload.HTTPMethod,
		ContentType: payload.ContentType,
		TargetBytes: payload.TargetBytes,
		TargetKiB:   payload.TargetKiB,
		TargetKB:    payload.TargetKB,
		ActualBytes: payload.ActualBytes,
		ActualKiB:   payload.ActualKiB,
		ActualKB:    payload.ActualKB,
	}
}

func newAuthMetadata(authCfg model.AuthConfig) *model.AuthMetadata {
	if !authCfg.Enabled {
		return nil
	}
	return &model.AuthMetadata{
		Mode:               authCfg.Mode,
		TokenURL:           normalizeAuthTokenURL(authCfg.TokenURL),
		ClientAuthMethod:   authCfg.ClientAuthMethod,
		RefreshSkewSeconds: authCfg.RefreshSkewSeconds,
		SecretSource:       authCfg.SecretSource,
	}
}

func normalizeAuthTokenURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err == nil {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}

	if idx := strings.IndexAny(trimmed, "?#"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	if schemeIdx := strings.Index(trimmed, "://"); schemeIdx >= 0 {
		prefix := trimmed[:schemeIdx+3]
		rest := trimmed[schemeIdx+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			trimmed = prefix + rest[at+1:]
		}
	}
	return trimmed
}

func refreshPayloadMetadata(metadata *model.TestMetadata, payloadContent string) {
	if metadata == nil || metadata.Payload == nil {
		return
	}
	if payloadContent == "" {
		if metadata.Payload.TargetBytes == 0 {
			metadata.Payload.ActualBytes = 0
			metadata.Payload.ActualKiB = 0
			metadata.Payload.ActualKB = 0
		}
		return
	}

	actualBytes := len([]byte(payloadContent))
	metadata.Payload.ActualBytes = actualBytes
	metadata.Payload.ActualKiB = float64(actualBytes) / 1024
	metadata.Payload.ActualKB = float64(actualBytes) / 1000
}

func (h *TestHandler) savePendingMeta(testID string, req model.TestRequest, warnings []model.ConflictWarning, executorType scriptgen.ExecutorType, controllable bool, payload *scriptgen.BuilderPayloadArtifacts) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.pendingMeta[testID] = &pendingTestInfo{
		Meta: &model.TestMetadata{
			Stages:    req.Stages,
			ScriptURL: req.URL,
			Payload:   newPayloadMetadata(payload),
			Auth:      newAuthMetadata(authConfigFromRuntimeInput(req.Auth)),
		},
		Warnings:     warnings,
		Executor:     string(executorType),
		Controllable: controllable,
	}
}

func (h *TestHandler) takePendingMeta(testID string) (*model.TestMetadata, []model.ConflictWarning) {
	h.mu.Lock()
	defer h.mu.Unlock()

	pm, ok := h.pendingMeta[testID]
	if !ok {
		return nil, nil
	}
	delete(h.pendingMeta, testID)
	return pm.Meta, pm.Warnings
}

func newTestMetadata(startTime, endTime time.Time, workerNames []string) *model.TestMetadata {
	missingWorkers := make([]string, 0, len(workerNames))
	for _, name := range workerNames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		missingWorkers = append(missingWorkers, name)
	}

	return &model.TestMetadata{
		StartedAt:   startTime,
		EndedAt:     endTime,
		DurationS:   endTime.Sub(startTime).Seconds(),
		WorkerCount: len(workerNames),
		ArtifactCollection: &model.ArtifactCollectionMetadata{
			Status:                     "missing",
			ExpectedWorkerCount:        len(workerNames),
			ReceivedWorkerSummaryCount: 0,
			MissingWorkers:             missingWorkers,
		},
	}
}

func artifactCollectionReason(collection *model.ArtifactCollectionMetadata) string {
	if collection == nil {
		return ""
	}

	switch collection.Status {
	case "complete":
		return fmt.Sprintf(
			"Received worker summaries for all %d expected workers.",
			collection.ExpectedWorkerCount,
		)
	case "partial":
		reason := fmt.Sprintf(
			"Received %d of %d expected worker summaries.",
			collection.ReceivedWorkerSummaryCount,
			collection.ExpectedWorkerCount,
		)
		if len(collection.MissingWorkers) > 0 {
			reason += " Missing workers: " + strings.Join(collection.MissingWorkers, ", ") + "."
		}
		return reason
	case "missing":
		if collection.ExpectedWorkerCount > 0 {
			return fmt.Sprintf("No worker summary artifacts were collected for %d expected workers.", collection.ExpectedWorkerCount)
		}
		return "No worker summary artifacts were collected before result persistence."
	default:
		return ""
	}
}

func classifyArtifactCollection(expectedWorkers []string, merged *scriptgen.MergedSummaryMetrics) *model.ArtifactCollectionMetadata {
	receivedNames := make(map[string]struct{}, len(expectedWorkers))
	receivedCount := 0
	if merged != nil {
		receivedCount = len(merged.Workers)
		for _, worker := range merged.Workers {
			name := strings.TrimSpace(worker.Name)
			if name == "" {
				continue
			}
			receivedNames[name] = struct{}{}
		}
	}

	expectedCount := len(expectedWorkers)
	if expectedCount == 0 && merged != nil {
		expectedCount = merged.RawWorkerCount
	}

	missingWorkers := make([]string, 0, expectedCount)
	for _, worker := range expectedWorkers {
		worker = strings.TrimSpace(worker)
		if worker == "" {
			continue
		}
		if _, ok := receivedNames[worker]; ok {
			continue
		}
		missingWorkers = append(missingWorkers, worker)
	}

	status := "missing"
	switch {
	case expectedCount == 0 && receivedCount > 0:
		status = "complete"
	case receivedCount <= 0:
		status = "missing"
	case expectedCount > 0 && receivedCount >= expectedCount && len(missingWorkers) == 0:
		status = "complete"
	default:
		status = "partial"
	}

	return &model.ArtifactCollectionMetadata{
		Status:                     status,
		ExpectedWorkerCount:        expectedCount,
		ReceivedWorkerSummaryCount: receivedCount,
		MissingWorkers:             missingWorkers,
	}
}

func artifactCollectionGraceWindow(metadata *model.TestMetadata) time.Duration {
	const (
		minGrace = 10 * time.Second
		maxGrace = 90 * time.Second
	)

	if metadata == nil {
		return minGrace
	}

	durationBased := time.Duration(metadata.DurationS * 0.15 * float64(time.Second))
	workerBonus := time.Duration(min(metadata.WorkerCount, 10)) * time.Second
	grace := durationBased + workerBonus

	if grace < minGrace {
		return minGrace
	}
	if grace > maxGrace {
		return maxGrace
	}
	return grace
}

func artifactCollectionDeadlines(now time.Time, window time.Duration, followUpReserve time.Duration, reserveForFollowUp bool) (time.Time, time.Time) {
	finalDeadline := now.Add(window)
	initialDeadline := finalDeadline

	if reserveForFollowUp && followUpReserve > 0 && window > followUpReserve {
		initialDeadline = finalDeadline.Add(-followUpReserve)
	}

	return initialDeadline, finalDeadline
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func summaryCollectionFromRaw(expectedWorkers []string, raw string, finalMetrics *model.AggregatedMetrics) (summaryCollectionResult, error) {
	if strings.TrimSpace(raw) == "" {
		return summaryCollectionResult{
			ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
		}, fmt.Errorf("empty raw summary content")
	}

	mergedSummary, err := scriptgen.ParseRawSummaryContent(raw)
	if err != nil {
		return summaryCollectionResult{
			ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
		}, err
	}

	applySummaryPercentiles(finalMetrics, &scriptgen.SummaryPercentiles{
		P90: mergedSummary.TotalLatency.P90,
		P95: mergedSummary.TotalLatency.P95,
		P99: mergedSummary.TotalLatency.P99,
	})

	return summaryCollectionResult{
		Raw:                raw,
		Loaded:             true,
		ArtifactCollection: classifyArtifactCollection(expectedWorkers, mergedSummary),
	}, nil
}

func (h *TestHandler) configureArtifactUploads(testID string, prepared *executionPreparation) error {
	if h.completionRegistry == nil {
		return nil
	}

	uploadToken := uuid.NewString()
	h.completionRegistry.RegisterRun(testID, h.orch.WorkerNames(), uploadToken)

	prepared.envVars["SHIVA_ARTIFACT_UPLOAD_ENABLED"] = "true"
	prepared.envVars["SHIVA_ARTIFACT_UPLOAD_URL"] = strings.TrimRight(h.internalControllerURL, "/")
	prepared.envVars["SHIVA_ARTIFACT_UPLOAD_TOKEN"] = uploadToken
	prepared.envVars["SHIVA_ARTIFACT_TEST_ID"] = testID
	prepared.envVars["SHIVA_ARTIFACT_UPLOAD_POST_RUN_GRACE_S"] = "5"
	prepared.envVars["SHIVA_ARTIFACT_UPLOAD_SETTLE_WINDOW_S"] = "2"
	prepared.envVars["SHIVA_ARTIFACT_UPLOAD_WATCH_TIMEOUT_S"] = "90"

	if err := scriptgen.WriteEnvFile(h.scriptsDir, prepared.envVars); err != nil {
		h.completionRegistry.RemoveRun(testID)
		return fmt.Errorf("failed to write env file for artifact uploads: %w", err)
	}

	return nil
}

func (h *TestHandler) loadUploadedSummary(testID string, finalMetrics *model.AggregatedMetrics, expectedWorkers []string) summaryCollectionResult {
	if h.completionRegistry == nil {
		return summaryCollectionResult{
			ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
		}
	}

	snapshot, ok := h.completionRegistry.Snapshot(testID)
	if !ok {
		return summaryCollectionResult{
			ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
		}
	}

	raw := completion.BuildRawSummary(snapshot, completion.ArtifactSummary)
	result, err := summaryCollectionFromRaw(expectedWorkers, raw, finalMetrics)
	if err != nil {
		return summaryCollectionResult{
			ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
		}
	}
	return result
}

func (h *TestHandler) mergePendingMeta(testID string, metadata *model.TestMetadata) []model.ConflictWarning {
	pendingMeta, warnings := h.takePendingMeta(testID)
	if pendingMeta == nil {
		return nil
	}

	metadata.Stages = pendingMeta.Stages
	metadata.ScriptURL = pendingMeta.ScriptURL
	metadata.Payload = pendingMeta.Payload
	metadata.Auth = pendingMeta.Auth
	return warnings
}

func buildCompletedResult(testID string, finalMetrics *model.AggregatedMetrics, metricsV2 *model.MetricsV2, timeSeries []model.TimePoint, metadata *model.TestMetadata, warnings []model.ConflictWarning, rawSummary string, rawAuthSummary string) model.TestResult {
	return model.TestResult{
		ID:                 testID,
		Metrics:            finalMetrics,
		MetricsV2:          metricsV2,
		TimeSeries:         timeSeries,
		Metadata:           metadata,
		Warnings:           warnings,
		SummaryContent:     rawSummary,
		AuthSummaryContent: rawAuthSummary,
		Status:             "completed",
	}
}

func (h *TestHandler) buildCompletionData(testID string, finalMetrics *model.AggregatedMetrics, endTime time.Time) completionData {
	h.orch.ApplyPeakVUs(finalMetrics)
	timeSeries := h.orch.GetTimeSeries()
	startTime := h.orch.GetTestStartTime()
	metadata := newTestMetadata(startTime, endTime, h.orch.WorkerNames())
	warnings := h.mergePendingMeta(testID, metadata)

	return completionData{
		metrics:    finalMetrics,
		timeSeries: timeSeries,
		metadata:   metadata,
		warnings:   warnings,
	}
}

func (h *TestHandler) applyCompletionAuthSummary(metadata *model.TestMetadata, authSummary *scriptgen.AuthSummaryData) {
	applyAuthSummaryToMetadata(metadata, authSummary)
}

func markAuthMetricsUnavailable(metadata *model.TestMetadata) {
	if metadata == nil || metadata.Auth == nil {
		return
	}
	if metadata.Auth.Metrics != nil {
		return
	}
	if metadata.Auth.MetricsStatus == "" {
		metadata.Auth.MetricsStatus = "unavailable"
	}
	if metadata.Auth.MetricsMessage == "" {
		metadata.Auth.MetricsMessage = "Authentication was configured for this run, but no auth summary artifact was produced."
	}
}

func (h *TestHandler) persistCompletedResult(ctx context.Context, testID string, data completionData, rawSummary string, rawAuthSummary string, payloadContent string, authSummary *scriptgen.AuthSummaryData) error {
	refreshPayloadMetadata(data.metadata, payloadContent)
	h.applyCompletionAuthSummary(data.metadata, authSummary)
	if authSummary == nil {
		markAuthMetricsUnavailable(data.metadata)
	}

	metricsV2 := buildMetricsV2(data.metrics, rawSummary, data.metadata)
	result := buildCompletedResult(testID, data.metrics, metricsV2, data.timeSeries, data.metadata, data.warnings, rawSummary, rawAuthSummary)
	if enriched, mergedPayloadContent, changed := mergeRegistryArtifactsIntoResult(result, h.completionRegistry, testID, payloadContent); changed {
		result = enriched
		payloadContent = mergedPayloadContent
		h.logger.Info("merged latest registry artifacts into completed result",
			"test_id", testID,
			"artifact_collection_status", artifactCollectionStatus(result.Metadata),
		)
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	if payloadContent != "" {
		if err := h.store.UpdateLoadTestPayloadContent(ctx, testID, payloadContent); err != nil {
			return fmt.Errorf("save payload artifact: %w", err)
		}
	}

	if err := h.store.UpdateLoadTestResult(ctx, testID, "completed", resultJSON); err != nil {
		return fmt.Errorf("save result: %w", err)
	}

	return nil
}

func artifactCollectionStatus(metadata *model.TestMetadata) string {
	if metadata == nil || metadata.ArtifactCollection == nil {
		return ""
	}
	return metadata.ArtifactCollection.Status
}

func (h *TestHandler) latestCompletionMetrics(ctx context.Context, testID string) *model.AggregatedMetrics {
	finalMetrics := h.orch.GetLatestMetrics()
	if finalMetrics == nil {
		h.logger.Warn("no metrics collected during test run, attempting fresh fetch", "test_id", testID)
		return h.orch.FetchFinalMetrics(ctx)
	}
	return finalMetrics
}

func (h *TestHandler) prepareExecution(req *model.TestRequest) (*executionPreparation, error) {
	hasScript := req.ScriptContent != ""
	hasBuilder := req.URL != ""

	h.orch.SetPhase(orchestrator.PhaseScript, "Preparing k6 script...")

	var scriptContent string
	var executorType scriptgen.ExecutorType
	if hasScript {
		if err := scriptgen.ValidateUpload(req.ScriptContent); err != nil {
			return nil, fmt.Errorf("invalid script: %s", err)
		}
		scriptContent = req.ScriptContent
		executorType = scriptgen.DetectExecutorFromScript(scriptContent)
	} else {
		result, err := scriptgen.GenerateFromBuilder(req, h.orch.WorkerCount())
		if err != nil {
			return nil, fmt.Errorf("script generation failed: %s", err)
		}
		scriptContent = result.Script
		executorType = result.ExecutorType
	}

	configContent := req.ConfigContent
	if hasBuilder {
		var err error
		if strings.TrimSpace(configContent) == "" {
			configContent, err = scriptgen.BuildBuilderConfig(req)
			if err != nil {
				return nil, fmt.Errorf("failed to generate builder config: %w", err)
			}
		} else {
			configContent, err = scriptgen.EnrichBuilderConfig(configContent, req)
			if err != nil {
				return nil, fmt.Errorf("failed to enrich builder config: %w", err)
			}
		}
	}
	if !hasScript && configContent != "" {
		scriptContent = scriptgen.StripScriptOptions(scriptContent)
	}

	prepared := &executionPreparation{
		scriptContent: scriptContent,
		envVars:       make(map[string]string),
		executorType:  executorType,
		controllable:  executorType.IsControllable(),
	}

	if hasBuilder {
		payloadArtifacts, err := scriptgen.BuildBuilderPayloadArtifacts(req)
		if err != nil {
			return nil, fmt.Errorf("invalid builder payload: %s", err)
		}
		prepared.payload = payloadArtifacts
	}

	if configContent != "" {
		processed, err := scriptgen.ValidateAndProcessConfig(configContent, h.orch.WorkerCount())
		if err != nil {
			return nil, fmt.Errorf("invalid config: %s", err)
		}
		prepared.configContent = configContent
		prepared.configStages = processed.Stages
		prepared.envVars = processed.EnvVars
		if hasScript && processed.ExecutorType != "" {
			prepared.executorType = processed.ExecutorType
			prepared.controllable = processed.ExecutorType.IsControllable()
		}
		prepared.conflictWarnings = scriptgen.CheckConflicts(scriptContent, processed.OptionsJSON)
		if err := scriptgen.WriteConfig(h.scriptsDir, processed.OptionsJSON); err != nil {
			return nil, fmt.Errorf("failed to write config: %w", err)
		}
		if len(prepared.configStages) > 0 {
			h.logger.Info("transformed VU-based scenarios to externally-controlled", "stages", len(prepared.configStages))
		}
	} else {
		_ = scriptgen.RemoveConfig(h.scriptsDir)
	}

	if hasBuilder && req.URL != "" {
		prepared.envVars["TARGET_URL"] = req.URL
	}
	if hasBuilder && req.Auth.Enabled && strings.TrimSpace(req.Auth.ClientSecret) != "" {
		prepared.envVars[scriptgen.AuthClientSecretEnvVar] = strings.TrimSpace(req.Auth.ClientSecret)
	}

	if len(prepared.envVars) > 0 {
		if err := scriptgen.WriteEnvFile(h.scriptsDir, prepared.envVars); err != nil {
			return nil, fmt.Errorf("failed to write env file: %w", err)
		}
	} else {
		_ = scriptgen.RemoveEnvFile(h.scriptsDir)
	}

	h.logger.Info("executor type resolved", "executor", string(prepared.executorType), "controllable", prepared.controllable,
		"target_url", prepared.envVars["TARGET_URL"], "builder_url", req.URL)

	prepared.scriptContent = scriptgen.InjectStatusCounters(prepared.scriptContent)
	scriptWithSummary := scriptgen.InjectSummaryExport(prepared.scriptContent)
	if err := scriptgen.WriteScript(h.scriptsDir, scriptWithSummary); err != nil {
		return nil, fmt.Errorf("failed to write script: %w", err)
	}

	return prepared, nil
}

func effectiveTestURL(req model.TestRequest, envVars map[string]string) string {
	if req.URL != "" {
		return req.URL
	}
	if v, ok := envVars["TARGET_URL"]; ok && v != "" {
		return v
	}
	if v, ok := envVars["BASE_URL"]; ok && v != "" {
		return v
	}
	return ""
}

func expectedRuntimeForExecution(req model.TestRequest, prepared *executionPreparation) time.Duration {
	if prepared == nil || prepared.controllable {
		return 0
	}
	if strings.TrimSpace(prepared.configContent) != "" {
		return scriptgen.EstimateConfiguredExecutionDuration(prepared.configContent)
	}
	switch prepared.executorType {
	case scriptgen.ExecutorConstantArrivalRate:
		if strings.TrimSpace(req.Duration) == "" {
			return time.Minute
		}
		return time.Duration(scriptgen.ParseK6Duration(req.Duration)) * time.Second
	case scriptgen.ExecutorRampingArrivalRate:
		total := 0
		for _, stage := range req.Stages {
			total += scriptgen.ParseK6Duration(stage.Duration)
		}
		return time.Duration(total) * time.Second
	default:
		return 0
	}
}
func (h *TestHandler) createRunningLoadTest(ctx context.Context, req model.TestRequest, userID int64, username string, prepared *executionPreparation) (string, error) {
	testID := uuid.New().String()
	lt := &model.LoadTest{
		ID:                testID,
		ProjectName:       req.ProjectName,
		URL:               effectiveTestURL(req, prepared.envVars),
		Status:            "running",
		Executor:          req.Executor,
		Stages:            req.Stages,
		VUs:               req.VUs,
		Duration:          req.Duration,
		Rate:              req.Rate,
		TimeUnit:          req.TimeUnit,
		PreAllocatedVUs:   req.PreAllocatedVUs,
		MaxVUs:            req.MaxVUs,
		SleepSeconds:      req.SleepSeconds,
		ScriptContent:     prepared.scriptContent,
		ConfigContent:     prepared.configContent,
		PayloadSourceJSON: req.PayloadJSON,
		HTTPMethod:        req.HTTPMethod,
		ContentType:       req.ContentType,
		AuthConfig:        authConfigFromRuntimeInput(req.Auth),
		UserID:            userID,
		Username:          username,
	}
	if prepared.payload != nil {
		lt.PayloadContent = prepared.payload.Content
	}

	if err := h.store.CreateLoadTest(ctx, lt); err != nil {
		return "", fmt.Errorf("failed to create load test record: %w", err)
	}

	return testID, nil
}

func (h *TestHandler) reloadWorkersForExecution(ctx context.Context) error {
	h.orch.SetPhase(orchestrator.PhaseWorkers, "Restarting workers to load new script...")

	if err := h.orch.StopAll(ctx); err != nil {
		h.logger.Warn("some workers failed to stop (may already be stopped)", "error", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 90*time.Second)
	defer waitCancel()

	h.orch.SetPhase(orchestrator.PhaseWorkers, "Waiting for k6 workers to reload...")

	if err := h.orch.WaitForAllReady(waitCtx); err != nil {
		h.orch.SetPhase(orchestrator.PhaseError, "Workers not ready: "+err.Error())
		return fmt.Errorf("workers not ready: %w", err)
	}

	return nil
}

func (h *TestHandler) startPreparedExecution(ctx context.Context, testID string, req model.TestRequest, prepared *executionPreparation) error {
	hasBuilder := req.URL != ""

	h.orch.SetPhase(orchestrator.PhaseRunning, "Running load test...")
	if err := h.orch.ResumeAllForStart(ctx, prepared.controllable); err != nil {
		_ = h.store.UpdateLoadTestResult(ctx, testID, "error", nil)
		h.orch.SetPhase(orchestrator.PhaseError, "Failed to start workers")
		return fmt.Errorf("failed to start workers: %w", err)
	}

	h.savePendingMeta(testID, req, prepared.conflictWarnings, prepared.executorType, prepared.controllable, prepared.payload)

	var rampingStages []model.Stage
	var hasManagedRamp bool
	if hasBuilder {
		rampingStages, hasManagedRamp = resolveBuilderRamping(&req)
	} else {
		rampingStages, hasManagedRamp = resolveUploadRamping(prepared.configStages)
	}

	if hasManagedRamp && len(rampingStages) > 0 {
		h.orch.Ramping.Start(rampingStages)
		h.logger.Info("ramping manager started", "stages", len(rampingStages), "managed_ramp", true)
	}

	expectedRunDuration := expectedRuntimeForExecution(req, prepared)
	h.orch.StartPolling(testID, prepared.controllable, hasManagedRamp, expectedRunDuration, h.onTestComplete)

	h.logger.Info("test started",
		"test_id", testID,
		"project", req.ProjectName,
		"executor", string(prepared.executorType),
		"controllable", prepared.controllable,
		"has_config", prepared.configContent != "",
	)

	return nil
}

func applySummaryPercentiles(finalMetrics *model.AggregatedMetrics, percentiles *scriptgen.SummaryPercentiles) {
	if finalMetrics == nil || percentiles == nil {
		return
	}

	finalMetrics.P99Latency = percentiles.P99
	if percentiles.P90 > 0 {
		finalMetrics.P90Latency = percentiles.P90
	}
	if percentiles.P95 > 0 {
		finalMetrics.P95Latency = percentiles.P95
	}
}

func (h *TestHandler) loadCompletionSummary(ctx context.Context, testID string, finalMetrics *model.AggregatedMetrics, expectedWorkers []string, deadline time.Time) summaryCollectionResult {
	const summaryPollDelay = 1 * time.Second

	if len(expectedWorkers) == 0 {
		expectedWorkers = h.orch.WorkerNames()
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return summaryCollectionResult{
			ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
		}
	}

	best := summaryCollectionResult{
		ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
	}

	h.logger.Info("collecting completion artifacts with bounded deadline",
		"test_id", testID,
		"expected_workers", len(expectedWorkers),
		"deadline_sec", int(remaining.Seconds()),
	)

	for attempt := 0; ; attempt++ {
		uploadedResult := h.loadUploadedSummary(testID, finalMetrics, expectedWorkers)
		if uploadedResult.Loaded {
			if uploadedResult.ArtifactCollection.Status == "complete" {
				h.logger.Info("summary percentiles loaded from worker uploads",
					"test_id", testID,
					"attempt", attempt+1,
					"received_workers", uploadedResult.ArtifactCollection.ReceivedWorkerSummaryCount,
					"expected_workers", uploadedResult.ArtifactCollection.ExpectedWorkerCount,
				)
				return uploadedResult
			}
			if uploadedResult.ArtifactCollection.ReceivedWorkerSummaryCount > best.ArtifactCollection.ReceivedWorkerSummaryCount {
				best = uploadedResult
			}
		}

		raw := scriptgen.ReadRawSummaries(h.outputDir)
		if strings.TrimSpace(raw) != "" {
			fileResult, err := summaryCollectionFromRaw(expectedWorkers, raw, finalMetrics)
			if err != nil {
				h.logger.Debug("summary files not parseable yet", "attempt", attempt+1, "error", err)
			} else {
				if fileResult.ArtifactCollection.ReceivedWorkerSummaryCount > best.ArtifactCollection.ReceivedWorkerSummaryCount {
					best = fileResult
				}

				if fileResult.ArtifactCollection.Status == "complete" {
					h.logger.Info("summary percentiles loaded",
						"test_id", testID,
						"attempt", attempt+1,
						"received_workers", fileResult.ArtifactCollection.ReceivedWorkerSummaryCount,
						"expected_workers", fileResult.ArtifactCollection.ExpectedWorkerCount,
					)
					return fileResult
				}

				h.logger.Debug("worker summaries incomplete, waiting for remaining artifacts",
					"test_id", testID,
					"attempt", attempt+1,
					"received", fileResult.ArtifactCollection.ReceivedWorkerSummaryCount,
					"expected", fileResult.ArtifactCollection.ExpectedWorkerCount,
					"missing_workers", strings.Join(fileResult.ArtifactCollection.MissingWorkers, ","),
				)
			}
		} else {
			h.logger.Debug("summary files not ready yet", "attempt", attempt+1)
		}

		if !time.Now().Before(deadline) {
			break
		}

		waitFor := summaryPollDelay
		if untilDeadline := time.Until(deadline); untilDeadline < waitFor {
			waitFor = untilDeadline
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			h.logger.Warn("completion artifact collection interrupted",
				"test_id", testID,
				"error", ctx.Err(),
			)
			if best.Loaded {
				return best
			}
			return summaryCollectionResult{
				ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
			}
		case <-timer.C:
		}
	}

	if best.Loaded {
		h.logger.Warn("persisting partial worker summary artifacts after retries",
			"test_id", testID,
			"deadline_sec", int(time.Until(deadline).Seconds()),
			"received_workers", best.ArtifactCollection.ReceivedWorkerSummaryCount,
			"expected_workers", best.ArtifactCollection.ExpectedWorkerCount,
			"missing_workers", strings.Join(best.ArtifactCollection.MissingWorkers, ","),
		)
		return best
	}

	return summaryCollectionResult{
		ArtifactCollection: classifyArtifactCollection(expectedWorkers, nil),
	}
}

func (h *TestHandler) loadCompletionPayloadArtifact(testID string) string {
	if h.completionRegistry != nil {
		if snapshot, ok := h.completionRegistry.Snapshot(testID); ok {
			if content := completion.FirstArtifactContent(snapshot, completion.ArtifactPayload); strings.TrimSpace(content) != "" {
				return content
			}
		}
	}
	return scriptgen.ReadPayloadArtifact(h.outputDir)
}

func (h *TestHandler) loadCompletionAuthSummary(testID string, metadata *model.TestMetadata) (*scriptgen.AuthSummaryData, string) {
	if !hasConfiguredAuth(metadata) {
		return nil, ""
	}
	if h.completionRegistry != nil {
		if snapshot, ok := h.completionRegistry.Snapshot(testID); ok {
			if raw := completion.BuildRawSummary(snapshot, completion.ArtifactAuthSummary); strings.TrimSpace(raw) != "" {
				authSummary, err := scriptgen.ParseRawAuthSummaryContent(raw)
				if err == nil {
					return authSummary, raw
				}
			}
		}
	}
	authSummary, err := scriptgen.ReadAndMergeAuthSummaries(h.outputDir)
	if err != nil {
		return nil, ""
	}
	return authSummary, scriptgen.ReadRawAuthSummaries(h.outputDir)
}

func (h *TestHandler) cleanupWorkersAfterCompletion(ctx context.Context) {
	if err := h.orch.StopAll(ctx); err != nil {
		h.logger.Warn("failed to stop workers during cleanup", "error", err)
	}
}

func (h *TestHandler) logCompletedTest(testID string, finalMetrics *model.AggregatedMetrics, data completionData) {
	h.logger.Info("test completed",
		"test_id", testID,
		"total_requests", finalMetrics.TotalRequests,
		"time_series_points", len(data.timeSeries),
		"duration_s", data.metadata.DurationS,
	)
}

func (h *TestHandler) notifyScheduleCompletion(testID string, status string, errMsg string) {
	h.mu.Lock()
	notifier := h.scheduleNotifier
	h.mu.Unlock()

	if notifier == nil {
		return
	}

	notifier.OnTestComplete(testID, status, errMsg)
}

func (h *TestHandler) requireActiveTest(w http.ResponseWriter) (string, bool) {
	testID := h.orch.GetActiveTestID()
	if testID == "" {
		httpError(w, "no test is currently running", http.StatusNotFound)
		return "", false
	}
	return testID, true
}

func (h *TestHandler) executeRampingAction(w http.ResponseWriter, r *http.Request, action string, successStatus string, run func(context.Context) error) (string, bool) {
	testID, ok := h.requireActiveTest(w)
	if !ok {
		return "", false
	}

	if err := run(r.Context()); err != nil {
		h.logger.Error("failed to "+action, "error", err)
		msg, status := classifyK6Error(err, action)
		httpError(w, msg, status)
		return "", false
	}

	h.logger.Info("test "+action+"d", "test_id", testID)
	writeJSON(w, http.StatusOK, map[string]string{"status": successStatus, "test_id": testID})
	return testID, true
}

// StartTest writes the k6 script, waits for workers, resumes them, starts
// background polling, and returns immediately with the test ID.
// StartTest is the HTTP handler for manual test execution via POST /api/run.
// It validates input, checks for schedule conflicts, then delegates to ExecuteTest.
func (h *TestHandler) StartTest(w http.ResponseWriter, r *http.Request) {
	var req model.TestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := validateStartRequest(&req); err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if activeID := h.orch.GetActiveTestID(); activeID != "" {
		httpError(w, "a test is already running: "+activeID, http.StatusConflict)
		return
	}

	confirm := r.URL.Query().Get("confirm") == "true"
	if conflict := checkManualStartConflict(r.Context(), h.store, confirm); conflict != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":             "manual test may conflict with a scheduled test",
			"conflict":          conflict,
			"confirm_available": true,
		})
		return
	}

	userID := middleware.GetUserID(r.Context())
	username := middleware.GetUsername(r.Context())

	testID, controllable, warnings, err := h.ExecuteTest(r.Context(), req, userID, username)
	if err != nil {
		h.logger.Error("test execution failed", "error", err)
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"test_id":      testID,
		"status":       "running",
		"controllable": controllable,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	writeJSON(w, http.StatusOK, resp)
}

// ExecuteTest is the core test execution logic used by both manual StartTest and the Scheduler.
// It generates/validates the script, writes configs, restarts workers, and starts polling.
// Returns the test ID, controllable flag, any config warnings, and an error.
func (h *TestHandler) ExecuteTest(ctx context.Context, req model.TestRequest, userID int64, username string) (string, bool, []model.ConflictWarning, error) {
	if err := validateExecutionRequest(&req); err != nil {
		return "", false, nil, err
	}

	if activeID := h.orch.GetActiveTestID(); activeID != "" {
		return "", false, nil, fmt.Errorf("a test is already running: %s", activeID)
	}

	prepared, err := h.prepareExecution(&req)
	if err != nil {
		return "", false, nil, err
	}

	scriptgen.CleanupSummaries(h.outputDir)
	scriptgen.CleanupPayloadArtifacts(h.outputDir)
	scriptgen.CleanupAuthSummaries(h.outputDir)

	testID, err := h.createRunningLoadTest(ctx, req, userID, username, prepared)
	if err != nil {
		return "", false, nil, err
	}

	if err := h.configureArtifactUploads(testID, prepared); err != nil {
		_ = h.store.UpdateLoadTestResult(ctx, testID, "error", nil)
		return "", false, nil, err
	}

	if err := h.reloadWorkersForExecution(ctx); err != nil {
		if h.completionRegistry != nil {
			h.completionRegistry.RemoveRun(testID)
		}
		_ = h.store.UpdateLoadTestResult(ctx, testID, "error", nil)
		return "", false, nil, err
	}

	if err := h.startPreparedExecution(ctx, testID, req, prepared); err != nil {
		if h.completionRegistry != nil {
			h.completionRegistry.RemoveRun(testID)
		}
		return "", false, nil, err
	}

	return testID, prepared.controllable, prepared.conflictWarnings, nil
}

// onTestComplete is called by the orchestrator when all workers have finished.
func (h *TestHandler) onTestComplete(ctx context.Context, testID string) {
	defer func() {
		if h.completionRegistry != nil {
			h.completionRegistry.RemoveRun(testID)
		}
	}()

	h.orch.Ramping.Stop()
	h.orch.SetPhase(orchestrator.PhaseCollecting, "Collecting final metrics and summary data...")

	// Use the last polled metrics (accumulated during the entire test)
	finalMetrics := h.latestCompletionMetrics(ctx, testID)
	endTime := time.Now()
	data := h.buildCompletionData(testID, finalMetrics, endTime)
	expectedWorkers := h.orch.WorkerNames()
	collectionWindow := artifactCollectionGraceWindow(data.metadata)
	collectionStart := time.Now()
	initialCollectionDeadline, collectionDeadline := artifactCollectionDeadlines(
		collectionStart,
		collectionWindow,
		5*time.Second,
		h.completionRegistry != nil,
	)

	h.orch.StopPolling()

	// Let workers finish naturally so k6 can execute handleSummary and the worker-side
	// watcher can upload artifacts before Docker restarts the container.
	h.logger.Info("waiting for workers to finish naturally before collecting artifacts", "test_id", testID)

	// Wait for k6 handleSummary files or uploaded artifacts. k6 exits when its duration
	// or managed ramp completes, then writes handleSummary and the watcher can upload
	// those files before the next worker process starts.
	summaryResult := h.loadCompletionSummary(ctx, testID, finalMetrics, expectedWorkers, initialCollectionDeadline)
	rawSummary, summaryLoaded := summaryResult.Raw, summaryResult.Loaded
	if data.metadata != nil {
		data.metadata.ArtifactCollection = summaryResult.ArtifactCollection
	}

	if h.completionRegistry != nil && (data.metadata == nil || data.metadata.ArtifactCollection == nil || data.metadata.ArtifactCollection.Status != "complete") {
		h.logger.Info("initial artifact collection incomplete, stopping workers and waiting for late uploads",
			"test_id", testID,
			"remaining_sec", int(time.Until(collectionDeadline).Seconds()),
		)
		h.cleanupWorkersAfterCompletion(ctx)

		if time.Now().Before(collectionDeadline) {
			followUp := h.loadCompletionSummary(ctx, testID, finalMetrics, expectedWorkers, collectionDeadline)
			if followUp.Loaded || followUp.ArtifactCollection.ReceivedWorkerSummaryCount > summaryResult.ArtifactCollection.ReceivedWorkerSummaryCount {
				summaryResult = followUp
				rawSummary = followUp.Raw
				summaryLoaded = followUp.Loaded
				if data.metadata != nil {
					data.metadata.ArtifactCollection = followUp.ArtifactCollection
				}
			}
		}
	} else {
		h.cleanupWorkersAfterCompletion(ctx)
	}

	payloadArtifact := h.loadCompletionPayloadArtifact(testID)
	authSummary, rawAuthSummary := h.loadCompletionAuthSummary(testID, data.metadata)
	if !summaryLoaded {
		status := ""
		if data.metadata != nil && data.metadata.ArtifactCollection != nil {
			status = data.metadata.ArtifactCollection.Status
		}
		h.logger.Warn("could not read summary files after retries, worker drilldown will be incomplete",
			"test_id", testID,
			"artifact_collection_status", status,
		)
	}

	h.logCompletedTest(testID, finalMetrics, data)

	if err := h.persistCompletedResult(ctx, testID, data, rawSummary, rawAuthSummary, payloadArtifact, authSummary); err != nil {
		h.logger.Error("failed to persist completed result", "error", err)
	}

	h.orch.SetPhase(orchestrator.PhaseDone, testID)
	h.notifyScheduleCompletion(testID, "completed", "")
}

// GetLiveMetrics returns the latest aggregated metrics and test phase.
func (h *TestHandler) GetLiveMetrics(w http.ResponseWriter, r *http.Request) {
	testID := h.orch.GetActiveTestID()
	phase, phaseMsg := h.orch.GetPhase()

	// If test just completed, still return the result.
	if phase == orchestrator.PhaseDone {
		writeJSON(w, http.StatusOK, map[string]any{
			"test_id": phaseMsg, // phaseMsg holds the testID when done
			"status":  "completed",
			"phase":   string(phase),
		})
		return
	}

	// Collecting phase: test finished but metrics/summary are being gathered.
	// activeTestID may already be cleared by StopPolling, so use phase to detect.
	if phase == orchestrator.PhaseCollecting {
		writeJSON(w, http.StatusOK, map[string]any{
			"test_id": testID,
			"status":  "running",
			"phase":   string(phase),
			"message": phaseMsg,
		})
		return
	}

	if testID == "" {
		httpError(w, "no test is currently running", http.StatusNotFound)
		return
	}

	metrics := h.orch.GetLatestMetrics()

	resp := map[string]any{
		"test_id": testID,
		"status":  "running",
		"phase":   string(phase),
		"message": phaseMsg,
	}
	if metrics != nil {
		resp["metrics"] = metrics
	}

	// Include executor info so frontend can show/hide controls
	h.mu.Lock()
	if pm, ok := h.pendingMeta[testID]; ok {
		resp["executor"] = pm.Executor
		resp["controllable"] = pm.Controllable
	}
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}

// UploadScript handles multipart file upload of a k6 script.
func (h *TestHandler) UploadScript(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		httpError(w, "file too large or invalid form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("script")
	if err != nil {
		httpError(w, "missing 'script' file field", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	content, err := io.ReadAll(file)
	if err != nil {
		httpError(w, "failed to read file", http.StatusInternalServerError)
		return
	}

	if err := scriptgen.ValidateUpload(string(content)); err != nil {
		httpError(w, fmt.Sprintf("invalid script: %s", err), http.StatusBadRequest)
		return
	}

	if err := scriptgen.WriteScript(h.scriptsDir, string(content)); err != nil {
		h.logger.Error("failed to write uploaded script", "error", err)
		httpError(w, "failed to save script", http.StatusInternalServerError)
		return
	}

	h.logger.Info("script uploaded", "filename", header.Filename, "size", header.Size)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "uploaded",
		"filename": header.Filename,
	})
}

// StopTest stops all workers and saves the final metrics including time series and metadata.
func (h *TestHandler) StopTest(w http.ResponseWriter, r *http.Request) {
	testID, ok := h.requireActiveTest(w)
	if !ok {
		return
	}

	h.orch.Ramping.Stop()

	finalMetrics := h.orch.FetchFinalMetrics(r.Context())
	endTime := time.Now()
	data := h.buildCompletionData(testID, finalMetrics, endTime)

	h.orch.StopPolling()

	if err := h.orch.StopAll(r.Context()); err != nil {
		h.logger.Error("failed to stop some workers", "error", err)
	}

	payloadArtifact := h.loadCompletionPayloadArtifact(testID)
	authSummary, rawAuthSummary := h.loadCompletionAuthSummary(testID, data.metadata)
	if err := h.persistCompletedResult(r.Context(), testID, data, "", rawAuthSummary, payloadArtifact, authSummary); err != nil {
		h.logger.Error("failed to persist completed result", "error", err)
		httpError(w, "internal error", http.StatusInternalServerError)
		return
	}

	if h.completionRegistry != nil {
		h.completionRegistry.RemoveRun(testID)
	}

	h.orch.SetPhase(orchestrator.PhaseDone, testID)
	h.notifyScheduleCompletion(testID, "completed", "")
	h.logger.Info("test stopped", "test_id", testID,
		"time_series_points", len(data.timeSeries), "duration_s", data.metadata.DurationS)
	writeJSON(w, http.StatusOK, buildCompletedResult(testID, data.metrics, buildMetricsV2(data.metrics, "", data.metadata), data.timeSeries, data.metadata, data.warnings, "", rawAuthSummary))
}

// classifyK6Error translates raw k6 REST API errors into user-friendly messages.
func classifyK6Error(err error, action string) (string, int) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "doesn't support pause and resume"):
		return "Pause/Resume is only supported for the Ramp Up/Down (Builder) executor. Native k6 executors manage their own schedule.", http.StatusConflict
	case strings.Contains(msg, "externally-controlled executor needs to be configured"):
		return "VU scaling is only available for the Ramp Up/Down (Builder) executor.", http.StatusConflict
	default:
		return fmt.Sprintf("Failed to %s: %s", action, msg), http.StatusBadGateway
	}
}

// PauseTest pauses VU ramping and all k6 workers.
func (h *TestHandler) PauseTest(w http.ResponseWriter, r *http.Request) {
	_, _ = h.executeRampingAction(w, r, "pause", "paused", h.orch.Ramping.Pause)
}

// ResumeTest resumes VU ramping and all k6 workers.
func (h *TestHandler) ResumeTest(w http.ResponseWriter, r *http.Request) {
	_, _ = h.executeRampingAction(w, r, "resume", "running", h.orch.Ramping.Resume)
}

// ScaleTest adjusts VU count across all workers.
func (h *TestHandler) ScaleTest(w http.ResponseWriter, r *http.Request) {
	testID, ok := h.requireActiveTest(w)
	if !ok {
		return
	}

	var req struct {
		VUs int `json:"vus"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.VUs < 1 {
		httpError(w, "vus must be at least 1", http.StatusBadRequest)
		return
	}

	// Tell the ramping manager about the manual override so it doesn't
	// immediately overwrite the user's VU choice on the next tick.
	h.orch.Ramping.SetManualOverride(req.VUs)

	if err := h.orch.ScaleVUs(r.Context(), req.VUs); err != nil {
		h.logger.Error("failed to scale VUs", "error", err)
		msg, status := classifyK6Error(err, "scale VUs")
		httpError(w, msg, status)
		return
	}

	h.logger.Info("VUs scaled", "test_id", testID, "total_vus", req.VUs)
	writeJSON(w, http.StatusOK, map[string]any{"status": "scaled", "test_id": testID, "vus": req.VUs})
}

// GetWorkerStatus returns the current status of all workers.
func (h *TestHandler) GetWorkerStatus(w http.ResponseWriter, r *http.Request) {
	workers := h.orch.CheckWorkers(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"workers":     workers,
		"active_test": h.orch.GetActiveTestID(),
	})
}

// resolveBuilderRamping determines ramping stages for Builder mode.
// Returns the stages and whether the RampingManager should control test lifecycle.
func resolveBuilderRamping(req *model.TestRequest) ([]model.Stage, bool) {
	switch req.Executor {
	case "ramping-vus":
		if len(req.Stages) > 0 {
			return req.Stages, true
		}
		return nil, false

	case "constant-vus":
		if req.VUs <= 0 {
			return nil, false
		}
		dur := req.Duration
		if dur == "" {
			dur = "1m"
		}
		// "0s" stage instantly jumps to target (calculateVUs starts at prevTarget=0).
		// Second stage holds constant for the full duration.
		return []model.Stage{
			{Duration: "0s", Target: req.VUs},
			{Duration: dur, Target: req.VUs},
		}, true

	default:
		// arrival-rate executors: no controller-managed ramping
		return nil, false
	}
}

// resolveUploadRamping determines ramping stages for Upload mode.
// Uses stages extracted from config (transformed VU-based scenarios).
func resolveUploadRamping(configStages []model.Stage) ([]model.Stage, bool) {
	if len(configStages) > 0 {
		return configStages, true
	}
	return nil, false
}
