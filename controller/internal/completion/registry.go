package completion

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type ArtifactType string

const (
	ArtifactSummary     ArtifactType = "summary"
	ArtifactAuthSummary ArtifactType = "auth-summary"
	ArtifactPayload     ArtifactType = "payload"
)

var (
	ErrUnknownRun      = errors.New("unknown run")
	ErrUnauthorized    = errors.New("unauthorized artifact upload")
	ErrUnknownWorker   = errors.New("unknown worker")
	ErrUnknownArtifact = errors.New("unknown artifact type")
)

type Artifact struct {
	WorkerID    string
	Content     string
	ContentType string
	ReceivedAt  time.Time
}

type Snapshot struct {
	TestID          string
	ExpectedWorkers []string
	UploadToken     string
	Artifacts       map[ArtifactType]map[string]Artifact
	CreatedAt       time.Time
}

type Registry struct {
	mu            sync.RWMutex
	runs          map[string]*runState
	lateUploadTTL time.Duration
}

type runState struct {
	testID          string
	expectedWorkers []string
	expectedSet     map[string]struct{}
	uploadToken     string
	artifacts       map[ArtifactType]map[string]Artifact
	createdAt       time.Time
	closedAt        time.Time
	expiresAt       time.Time
}

func NewRegistry() *Registry {
	return newRegistryWithTTL(5 * time.Minute)
}

func newRegistryWithTTL(lateUploadTTL time.Duration) *Registry {
	if lateUploadTTL <= 0 {
		lateUploadTTL = 5 * time.Minute
	}
	return &Registry{
		runs:          make(map[string]*runState),
		lateUploadTTL: lateUploadTTL,
	}
}

func (r *Registry) RegisterRun(testID string, expectedWorkers []string, uploadToken string) {
	expectedSet := make(map[string]struct{}, len(expectedWorkers))
	normalizedWorkers := make([]string, 0, len(expectedWorkers))
	for _, worker := range expectedWorkers {
		worker = strings.TrimSpace(worker)
		if worker == "" {
			continue
		}
		if _, exists := expectedSet[worker]; exists {
			continue
		}
		expectedSet[worker] = struct{}{}
		normalizedWorkers = append(normalizedWorkers, worker)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupExpiredLocked(time.Now())
	r.runs[testID] = &runState{
		testID:          testID,
		expectedWorkers: normalizedWorkers,
		expectedSet:     expectedSet,
		uploadToken:     uploadToken,
		artifacts: map[ArtifactType]map[string]Artifact{
			ArtifactSummary:     {},
			ArtifactAuthSummary: {},
			ArtifactPayload:     {},
		},
		createdAt: time.Now(),
	}
}

func (r *Registry) RemoveRun(testID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupExpiredLocked(time.Now())
	run, ok := r.runs[testID]
	if !ok {
		return
	}
	now := time.Now()
	run.closedAt = now
	run.expiresAt = now.Add(r.lateUploadTTL)
}

func (r *Registry) StoreArtifact(testID, workerID, uploadToken string, artifactType ArtifactType, contentType string, content []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupExpiredLocked(time.Now())

	run, ok := r.runs[testID]
	if !ok {
		return ErrUnknownRun
	}
	if run.uploadToken == "" || uploadToken != run.uploadToken {
		return ErrUnauthorized
	}
	if _, ok := run.expectedSet[workerID]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownWorker, workerID)
	}
	if _, ok := run.artifacts[artifactType]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownArtifact, artifactType)
	}

	if artifactType == ArtifactAuthSummary {
		if existing, ok := run.artifacts[artifactType][workerID]; ok {
			existingSignal := authSummarySignal(existing.Content)
			newSignal := authSummarySignal(string(content))
			if newSignal <= existingSignal {
				// Keep the stronger (or equally strong) auth artifact to avoid late
				// no-op uploads from restarted workers erasing abort diagnostics.
				return nil
			}
		}
	}

	run.artifacts[artifactType][workerID] = Artifact{
		WorkerID:    workerID,
		Content:     string(content),
		ContentType: contentType,
		ReceivedAt:  time.Now(),
	}
	return nil
}

type authSummarySnapshot struct {
	Status  string `json:"status"`
	Metrics struct {
		TokenRequestsTotal  float64 `json:"token_requests_total"`
		TokenFailureTotal   float64 `json:"token_failure_total"`
		ResponseStatusCodes []struct {
			Code  int     `json:"code"`
			Count float64 `json:"count"`
		} `json:"response_status_codes"`
		AbortTriggered bool   `json:"abort_triggered"`
		AbortCause     string `json:"abort_cause"`
		AbortReason    string `json:"abort_reason"`
	} `json:"metrics"`
}

func authSummarySignal(content string) int {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return 0
	}

	var snapshot authSummarySnapshot
	if err := json.Unmarshal([]byte(trimmed), &snapshot); err != nil {
		// Keep malformed payloads at the lowest non-zero signal so a later valid
		// auth summary can replace them.
		return 1
	}

	signal := 1
	if strings.TrimSpace(snapshot.Status) != "" {
		signal += 1
	}
	if snapshot.Metrics.TokenRequestsTotal > 0 {
		signal += 10
	}
	if snapshot.Metrics.TokenFailureTotal > 0 {
		signal += 10
	}
	if len(snapshot.Metrics.ResponseStatusCodes) > 0 {
		signal += 5
	}
	if snapshot.Metrics.AbortTriggered {
		signal += 100
	}
	if strings.TrimSpace(snapshot.Metrics.AbortCause) != "" {
		signal += 5
	}
	if strings.TrimSpace(snapshot.Metrics.AbortReason) != "" {
		signal += 5
	}

	return signal
}

func (r *Registry) Snapshot(testID string) (Snapshot, bool) {
	r.mu.RLock()
	run, ok := r.runs[testID]
	r.mu.RUnlock()

	if !ok {
		return Snapshot{}, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupExpiredLocked(time.Now())

	run, ok = r.runs[testID]
	if !ok {
		return Snapshot{}, false
	}

	artifacts := make(map[ArtifactType]map[string]Artifact, len(run.artifacts))
	for artifactType, byWorker := range run.artifacts {
		copyMap := make(map[string]Artifact, len(byWorker))
		for workerID, artifact := range byWorker {
			copyMap[workerID] = artifact
		}
		artifacts[artifactType] = copyMap
	}

	expectedWorkers := make([]string, len(run.expectedWorkers))
	copy(expectedWorkers, run.expectedWorkers)

	return Snapshot{
		TestID:          run.testID,
		ExpectedWorkers: expectedWorkers,
		UploadToken:     run.uploadToken,
		Artifacts:       artifacts,
		CreatedAt:       run.createdAt,
	}, true
}

func (r *Registry) cleanupExpiredLocked(now time.Time) {
	for testID, run := range r.runs {
		if run == nil {
			delete(r.runs, testID)
			continue
		}
		if run.expiresAt.IsZero() {
			continue
		}
		if now.After(run.expiresAt) {
			delete(r.runs, testID)
		}
	}
}

func BuildRawSummary(snapshot Snapshot, artifactType ArtifactType) string {
	artifacts, ok := snapshot.Artifacts[artifactType]
	if !ok || len(artifacts) == 0 {
		return ""
	}

	orderedWorkers := orderedArtifactWorkers(snapshot.ExpectedWorkers, artifacts)
	parts := make([]string, 0, len(orderedWorkers))
	for _, workerID := range orderedWorkers {
		artifact, ok := artifacts[workerID]
		if !ok || strings.TrimSpace(artifact.Content) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("--- %s ---\n%s", workerID, artifact.Content))
	}

	return strings.Join(parts, "\n\n")
}

func FirstArtifactContent(snapshot Snapshot, artifactType ArtifactType) string {
	artifacts, ok := snapshot.Artifacts[artifactType]
	if !ok || len(artifacts) == 0 {
		return ""
	}

	for _, workerID := range orderedArtifactWorkers(snapshot.ExpectedWorkers, artifacts) {
		artifact, ok := artifacts[workerID]
		if !ok || strings.TrimSpace(artifact.Content) == "" {
			continue
		}
		return artifact.Content
	}

	return ""
}

func orderedArtifactWorkers(expectedWorkers []string, artifacts map[string]Artifact) []string {
	ordered := make([]string, 0, len(artifacts))
	seen := make(map[string]struct{}, len(artifacts))
	for _, workerID := range expectedWorkers {
		if _, ok := artifacts[workerID]; !ok {
			continue
		}
		ordered = append(ordered, workerID)
		seen[workerID] = struct{}{}
	}

	extras := make([]string, 0, len(artifacts))
	for workerID := range artifacts {
		if _, ok := seen[workerID]; ok {
			continue
		}
		extras = append(extras, workerID)
	}
	sort.Strings(extras)
	return append(ordered, extras...)
}
