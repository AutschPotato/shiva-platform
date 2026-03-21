package completion

import (
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
	mu   sync.RWMutex
	runs map[string]*runState
}

type runState struct {
	testID          string
	expectedWorkers []string
	expectedSet     map[string]struct{}
	uploadToken     string
	artifacts       map[ArtifactType]map[string]Artifact
	createdAt       time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		runs: make(map[string]*runState),
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
	delete(r.runs, testID)
}

func (r *Registry) StoreArtifact(testID, workerID, uploadToken string, artifactType ArtifactType, contentType string, content []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

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

	run.artifacts[artifactType][workerID] = Artifact{
		WorkerID:    workerID,
		Content:     string(content),
		ContentType: contentType,
		ReceivedAt:  time.Now(),
	}
	return nil
}

func (r *Registry) Snapshot(testID string) (Snapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	run, ok := r.runs[testID]
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
