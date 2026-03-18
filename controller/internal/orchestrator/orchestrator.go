package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
	"golang.org/x/sync/errgroup"
)

// WorkerResult holds the metrics fetched from a single worker.
type WorkerResult struct {
	Address string
	Metrics map[string]model.K6Metric
	Error   error
}

// TestPhase tracks the current phase of a running test for the frontend to poll.
type TestPhase string

const (
	PhaseScript     TestPhase = "script"
	PhaseWorkers    TestPhase = "workers"
	PhaseRunning    TestPhase = "running"
	PhaseCollecting TestPhase = "collecting"
	PhaseDone       TestPhase = "done"
	PhaseError      TestPhase = "error"
)

// CompletionFunc is called by the poll loop when all workers have finished.
type CompletionFunc func(ctx context.Context, testID string)

type DashboardRuntimeConfig struct {
	Enabled bool
	Host    string
	Port    int
}

// Orchestrator manages multiple k6 worker instances.
type Orchestrator struct {
	workers []*Worker
	logger  *slog.Logger

	mu             sync.RWMutex
	latestMetrics  *model.AggregatedMetrics
	timeSeries     []model.TimePoint
	testStartTime  time.Time
	activeTestID   string
	cancelPoll     context.CancelFunc
	pollInterval   time.Duration
	phase          TestPhase
	phaseMessage   string
	rampingDone    bool           // set when RampingManager finishes all stages
	controllable   bool           // true if executor supports Pause/Resume/Scale
	hasManagedRamp bool           // true if RampingManager controls test lifecycle
	seenRunning    bool           // true once we've seen workers actively executing
	zeroMetricRun  int            // consecutive ticks with 0 VUs after seenRunning (native executors)
	peakWorkerVUs    map[string]int // tracks peak VUs per worker address
	maxTestDuration  time.Duration  // absolute safety timeout for any test
	dashboard        DashboardRuntimeConfig

	Ramping *RampingManager
}

type pollStateSnapshot struct {
	seenRunning   bool
	controllable  bool
	hasManagedRamp bool
	zeroMetricRun int
	rampingDone   bool
}

func New(addresses []string, pollInterval time.Duration, maxTestDuration time.Duration, logger *slog.Logger, dashboard DashboardRuntimeConfig) *Orchestrator {
	workers := make([]*Worker, len(addresses))
	for i, addr := range addresses {
		workers[i] = NewWorker(addr, dashboard.Enabled, dashboard.Host, dashboard.Port)
	}
	if maxTestDuration <= 0 {
		maxTestDuration = 2 * time.Hour
	}
	o := &Orchestrator{
		workers:         workers,
		logger:          logger,
		pollInterval:    pollInterval,
		maxTestDuration: maxTestDuration,
		dashboard:       dashboard,
	}
	o.Ramping = NewRampingManager(o, logger)
	return o
}

// SetPhase updates the current test phase (visible to frontend via polling).
func (o *Orchestrator) SetPhase(phase TestPhase, message string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.phase = phase
	o.phaseMessage = message
}

// GetPhase returns the current test phase and message.
func (o *Orchestrator) GetPhase() (TestPhase, string) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.phase, o.phaseMessage
}

// ResumeAll sends an unpause command to all workers in parallel.
// Each worker is retried up to 5 times with 2s backoff to handle the case where
// the externally-controlled executor hasn't fully initialized yet.
func (o *Orchestrator) ResumeAll(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	paused := false
	patch := model.K6StatusPatch{Paused: &paused}

	for _, w := range o.workers {
		w := w
		g.Go(func() error {
			var lastErr error
			for attempt := 0; attempt < 5; attempt++ {
				if attempt > 0 {
					o.logger.Warn("retrying resume", "worker", w.Address, "attempt", attempt+1, "error", lastErr)
					time.Sleep(2 * time.Second)
				}
				_, err := w.PatchStatus(ctx, patch)
				if err == nil {
					o.logger.Info("worker resumed", "worker", w.Address)
					return nil
				}
				lastErr = err
			}
			o.logger.Error("failed to resume worker after retries", "worker", w.Address, "error", lastErr)
			return lastErr
		})
	}
	return g.Wait()
}

// PauseAll sends a pause command to all workers in parallel.
func (o *Orchestrator) PauseAll(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	paused := true
	patch := model.K6StatusPatch{Paused: &paused}

	for _, w := range o.workers {
		w := w
		g.Go(func() error {
			_, err := w.PatchStatus(ctx, patch)
			if err != nil {
				o.logger.Error("failed to pause worker", "worker", w.Address, "error", err)
				return err
			}
			o.logger.Info("worker paused", "worker", w.Address)
			return nil
		})
	}
	return g.Wait()
}

// ScaleVUs sends a VU count update to all workers in parallel.
// The target VUs are split evenly across workers.
func (o *Orchestrator) ScaleVUs(ctx context.Context, totalVUs int) error {
	perWorker := totalVUs / len(o.workers)
	remainder := totalVUs % len(o.workers)

	g, ctx := errgroup.WithContext(ctx)
	for i, w := range o.workers {
		w := w
		vus := perWorker
		if i < remainder {
			vus++
		}
		g.Go(func() error {
			_, err := w.PatchStatus(ctx, model.K6StatusPatch{VUs: &vus})
			if err != nil {
				o.logger.Error("failed to scale worker", "worker", w.Address, "target_vus", vus, "error", err)
				return err
			}
			o.logger.Info("worker scaled", "worker", w.Address, "vus", vus)
			return nil
		})
	}
	return g.Wait()
}

// ApplyPeakVUs replaces the current VU values in the metrics with the peak VUs
// observed during the test. This is useful for results display since the final
// VU count is often 0 (after ramp-down).
func (o *Orchestrator) ApplyPeakVUs(metrics *model.AggregatedMetrics) {
	if metrics == nil {
		return
	}
	o.mu.RLock()
	defer o.mu.RUnlock()

	totalPeak := 0
	for i := range metrics.Workers {
		wm := &metrics.Workers[i]
		if peak, ok := o.peakWorkerVUs[wm.Address]; ok {
			wm.VUs = peak
			totalPeak += peak
		}
	}
	metrics.TotalVUs = totalPeak
}

// SetRampingDone marks that all ramping stages have completed and workers were stopped.
// This tells allWorkersEnded() to ignore paused workers (which are Docker-restarted, not user-paused).
func (o *Orchestrator) SetRampingDone() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rampingDone = true
}

func (o *Orchestrator) clearActiveRunLocked() {
	if o.cancelPoll != nil {
		o.cancelPoll()
		o.cancelPoll = nil
	}
	o.activeTestID = ""
}

// StopAll sends a stop command to all workers in parallel.
func (o *Orchestrator) StopAll(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	stopped := true
	patch := model.K6StatusPatch{Stopped: &stopped}

	for _, w := range o.workers {
		w := w
		g.Go(func() error {
			_, err := w.PatchStatus(ctx, patch)
			if err != nil {
				o.logger.Error("failed to stop worker", "worker", w.Address, "error", err)
				return err
			}
			o.logger.Info("worker stopped", "worker", w.Address)
			return nil
		})
	}

	o.mu.Lock()
	o.clearActiveRunLocked()
	o.mu.Unlock()

	return g.Wait()
}

// StartPolling begins polling all workers for metrics at the configured interval.
// controllable indicates whether the executor supports Pause/Resume/Scale.
// hasManagedRamp indicates whether the RampingManager controls test lifecycle
// (end-detection waits for rampDone instead of checking worker status).
func (o *Orchestrator) StartPolling(testID string, controllable bool, hasManagedRamp bool, onComplete CompletionFunc) {
	o.mu.Lock()
	if o.cancelPoll != nil {
		o.cancelPoll()
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.resetPollingStateLocked(testID, cancel, controllable, hasManagedRamp)
	o.mu.Unlock()

	go o.pollLoop(ctx, testID, onComplete)
}

// StopPolling stops the metrics polling loop and clears the active test.
func (o *Orchestrator) StopPolling() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.clearActiveRunLocked()
}

func (o *Orchestrator) resetPollingStateLocked(testID string, cancel context.CancelFunc, controllable bool, hasManagedRamp bool) {
	o.cancelPoll = cancel
	o.activeTestID = testID
	o.timeSeries = nil
	o.testStartTime = time.Now()
	o.latestMetrics = nil
	o.rampingDone = false
	o.controllable = controllable
	o.hasManagedRamp = hasManagedRamp
	o.seenRunning = false
	o.zeroMetricRun = 0
	o.peakWorkerVUs = make(map[string]int)
}

func (o *Orchestrator) appendTimeSeriesPointLocked(agg *model.AggregatedMetrics) {
	elapsed := time.Since(o.testStartTime).Seconds()
	o.timeSeries = append(o.timeSeries, model.TimePoint{
		ElapsedSec:    elapsed,
		TotalVUs:      agg.TotalVUs,
		RPS:           agg.RPS,
		AvgLatency:    agg.AvgLatency,
		P95Latency:    agg.P95Latency,
		TotalRequests: agg.TotalRequests,
		BusinessRequests: agg.BusinessRequests,
		ErrorRate:     agg.ErrorRate,
		Status4xx:     agg.Status4xx,
		Status5xx:     agg.Status5xx,
	})
}

func (o *Orchestrator) updatePollingState(agg *model.AggregatedMetrics) pollStateSnapshot {
	hasData := agg.TotalRequests > 0 || agg.TotalVUs > 0

	o.mu.Lock()
	defer o.mu.Unlock()

	// Always update latestMetrics so the frontend receives data
	// on every poll — even before VUs ramp up or produce requests.
	o.latestMetrics = agg

	if hasData {
		if !o.seenRunning {
			o.seenRunning = true
			o.logger.Info("workers producing metrics — test is active",
				"total_vus", agg.TotalVUs, "total_reqs", agg.TotalRequests)
		}
		o.zeroMetricRun = 0

		for i := range agg.Workers {
			wm := &agg.Workers[i]
			if wm.VUs > o.peakWorkerVUs[wm.Address] {
				o.peakWorkerVUs[wm.Address] = wm.VUs
			}
		}

		o.appendTimeSeriesPointLocked(agg)
	} else if o.seenRunning {
		o.zeroMetricRun++
	}

	return pollStateSnapshot{
		seenRunning:   o.seenRunning,
		controllable:  o.controllable,
		hasManagedRamp: o.hasManagedRamp,
		zeroMetricRun: o.zeroMetricRun,
		rampingDone:   o.rampingDone,
	}
}

func (o *Orchestrator) shouldCompleteTest(ctx context.Context, tickCount int, state pollStateSnapshot) bool {
	const minTicksBeforeEndCheck = 5
	const zeroTicksToEnd = 3

	if state.hasManagedRamp {
		return state.rampingDone
	}
	if state.controllable {
		return state.seenRunning && tickCount >= minTicksBeforeEndCheck && o.allWorkersEnded(ctx)
	}
	if !state.seenRunning {
		return false
	}
	if state.zeroMetricRun >= zeroTicksToEnd {
		return true
	}
	// Native executors can stay artificially alive when the k6 web dashboard
	// keeps the process open after load generation has finished. In that case
	// VUs in the metrics snapshot may never drop to zero, but /v1/status already
	// reports the workers as paused/finished. Allow the status path to end the
	// run once the test has definitely started.
	return tickCount >= minTicksBeforeEndCheck && o.allWorkersEnded(ctx)
}

func (o *Orchestrator) pollLoop(ctx context.Context, testID string, onComplete CompletionFunc) {
	ticker := time.NewTicker(o.pollInterval)
	defer ticker.Stop()

	// Absolute safety timeout — no test may run longer than this.
	absoluteDeadline := time.NewTimer(o.maxTestDuration)
	defer absoluteDeadline.Stop()

	tickCount := 0
	const neverStartedTimeout = 150 // ~5 minutes at 2s interval

	for {
		select {
		case <-ctx.Done():
			return
		case <-absoluteDeadline.C:
			o.logger.Warn("test exceeded maximum duration, forcing completion",
				"test_id", testID,
				"max_duration", o.maxTestDuration,
				"tick_count", tickCount,
			)
			if onComplete != nil {
				onComplete(context.Background(), testID)
			}
			return
		case <-ticker.C:
			tickCount++

			metrics := o.fetchAllMetrics(ctx)
			agg := Aggregate(metrics)
			state := o.updatePollingState(agg)

			// End detection is three-way to avoid interference between Builder and Upload paths:
			//
			// 1. hasManagedRamp: RampingManager controls lifecycle (Builder VU-types, Upload with config stages).
			//    ONLY rampDone ends the test. This prevents premature termination while stages are active.
			//
			// 2. controllable without managed ramp: Upload of externally-controlled script without config.
			//    Workers manage their own duration; check allWorkersEnded() after a grace period.
			//
			// 3. Native (arrival-rate): prefer metrics-based detection, but fall back
			//    to worker status once the run has definitely started. This avoids
			//    dashboard-held processes keeping the controller in "running" forever.
			ended := o.shouldCompleteTest(ctx, tickCount, state)

			if tickCount%5 == 0 {
				o.logger.Info("end-check tick",
					"tick", tickCount,
					"ended", ended,
					"seenRunning", state.seenRunning,
					"zeroMetricRun", state.zeroMetricRun,
					"controllable", state.controllable,
					"managedRamp", state.hasManagedRamp,
					"rampDone", state.rampingDone,
					"total_vus", agg.TotalVUs,
					"total_reqs", agg.TotalRequests,
				)
			}
			if ended {
				o.logger.Info("all workers ended, completing test",
					"test_id", testID,
					"final_total_requests", agg.TotalRequests,
					"time_series_points", len(o.timeSeries),
				)
				if onComplete != nil {
					onComplete(context.Background(), testID)
				}
				return
			}

			// Safety guard: if test never produced any metrics after a grace period,
			// it likely failed to start (broken script, 0 VUs, etc.). Abort early
			// rather than waiting for the absolute deadline.
			if !state.seenRunning && tickCount >= neverStartedTimeout {
				o.logger.Warn("test never produced metrics after grace period, aborting",
					"test_id", testID,
					"tick_count", tickCount,
					"grace_seconds", neverStartedTimeout*int(o.pollInterval.Seconds()),
				)
				if onComplete != nil {
					onComplete(context.Background(), testID)
				}
				return
			}
		}
	}
}

// allWorkersEnded checks if all workers have finished their test run.
// For controllable (externally-controlled) executors:
//   - Paused workers are NOT ended (user intentionally paused)
//   - Exception: rampingDone means controller completed all stages
//
// For native executors (constant-vus, ramping-vus, arrival-rate):
//   - k6 finishes → exits → Docker restarts → new k6 starts paused
//   - Paused workers ARE considered ended (Docker restart, not user pause)
func (o *Orchestrator) allWorkersEnded(ctx context.Context) bool {
	o.mu.RLock()
	rampDone := o.rampingDone
	isControllable := o.controllable
	o.mu.RUnlock()

	// Ramping completed — workers were intentionally stopped, test is done
	if rampDone {
		return true
	}

	for _, w := range o.workers {
		status, err := w.GetStatus(ctx)
		if err != nil {
			o.logger.Debug("allWorkersEnded: worker unreachable", "worker", w.Address, "error", err)
			continue
		}

		o.logger.Debug("allWorkersEnded: worker status",
			"worker", w.Address,
			"paused", status.Paused,
			"running", status.IsRunning(),
			"finished", status.IsFinished(),
			"stopped", status.Stopped,
			"rawRunning", status.Running,
			"controllable", isControllable,
			"status", string(status.Status),
		)

		// Check paused FIRST: meaning depends on executor type
		if status.Paused {
			if isControllable {
				// Controllable: user intentionally paused → test still active
				return false
			}
			// Native executor: paused = Docker restarted k6 after exit → test ended
			continue
		}

		// Not paused: check if still actively running
		if status.IsRunning() {
			return false
		}
	}
	return true
}

// fetchAllMetrics fetches metrics from all workers in parallel.
func (o *Orchestrator) fetchAllMetrics(ctx context.Context) []WorkerResult {
	results := make([]WorkerResult, len(o.workers))
	var wg sync.WaitGroup

	for i, w := range o.workers {
		i, w := i, w
		wg.Add(1)
		go func() {
			defer wg.Done()
			metrics, err := w.GetMetrics(ctx)
			results[i] = WorkerResult{
				Address: w.Address,
				Metrics: metrics,
				Error:   err,
			}
		}()
	}
	wg.Wait()
	return results
}

// GetLatestMetrics returns the most recently aggregated metrics.
func (o *Orchestrator) GetLatestMetrics() *model.AggregatedMetrics {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.latestMetrics
}

// GetTimeSeries returns a copy of the collected time-series data.
func (o *Orchestrator) GetTimeSeries() []model.TimePoint {
	o.mu.RLock()
	defer o.mu.RUnlock()
	cp := make([]model.TimePoint, len(o.timeSeries))
	copy(cp, o.timeSeries)
	return cp
}

// GetTestStartTime returns the time the current test started.
func (o *Orchestrator) GetTestStartTime() time.Time {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.testStartTime
}

// WorkerCount returns the number of configured workers.
func (o *Orchestrator) WorkerCount() int {
	return len(o.workers)
}

func (o *Orchestrator) FindWorker(nameOrAddress string) *Worker {
	for _, w := range o.workers {
		if w == nil {
			continue
		}
		if w.Address == nameOrAddress || w.Name() == nameOrAddress {
			return w
		}
	}
	return nil
}

// GetActiveTestID returns the ID of the currently running test.
func (o *Orchestrator) GetActiveTestID() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.activeTestID
}

// FetchFinalMetrics does one final metrics fetch and aggregation.
func (o *Orchestrator) FetchFinalMetrics(ctx context.Context) *model.AggregatedMetrics {
	results := o.fetchAllMetrics(ctx)
	return Aggregate(results)
}

func workerDisplayStatus(status *model.K6Status, testActive bool) string {
	if status.IsRunning() && !status.Paused {
		return "running"
	}
	if status.Paused {
		return "paused"
	}
	if status.IsFinished() {
		if testActive {
			return "done"
		}
		return "online"
	}
	return "online"
}

func buildWorkerMetrics(address string, status *model.K6Status, err error, testActive bool) model.WorkerMetrics {
	return buildWorkerMetricsFromWorker(NewWorker(address, false, "", 0), status, err, testActive)
}

func buildWorkerMetricsFromWorker(worker *Worker, status *model.K6Status, err error, testActive bool) model.WorkerMetrics {
	wm := model.WorkerMetrics{
		Name:             worker.Name(),
		Address:          worker.Address,
		Status:           "unreachable",
		DashboardEnabled: worker.DashboardEnabled,
		DashboardURL:     worker.DashboardURL(),
	}
	if err != nil {
		wm.Error = err.Error()
		return wm
	}

	wm.Status = workerDisplayStatus(status, testActive)
	wm.VUs = status.VUs
	return wm
}

// WaitForAllReady blocks until every worker is reachable AND reports paused=true.
// k6 starts with --paused, but the externally-controlled executor needs a moment
// to initialize before it accepts pause/resume commands. Checking for paused=true
// ensures the executor is fully ready.
func (o *Orchestrator) WaitForAllReady(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		allReady := true
		for _, w := range o.workers {
			if !w.IsPaused(ctx) {
				allReady = false
				o.logger.Debug("worker not yet paused", "worker", w.Address)
				break
			}
		}
		if allReady {
			o.logger.Info("all workers ready and paused")
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for workers to become ready (paused)")
		case <-ticker.C:
		}
	}
}

// CheckWorkers returns the reachability status of all workers.
// When a test is active, k6 workers that report "finished" are re-labeled
// "done" to distinguish "worker finished its part of an active test" from
// "online" (no test loaded, truly idle).
func (o *Orchestrator) CheckWorkers(ctx context.Context) []model.WorkerMetrics {
	testActive := o.GetActiveTestID() != ""

	statuses := make([]model.WorkerMetrics, len(o.workers))
	var wg sync.WaitGroup

	for i, w := range o.workers {
		i, w := i, w
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := w.GetStatus(ctx)
			statuses[i] = buildWorkerMetricsFromWorker(w, status, err, testActive)
		}()
	}
	wg.Wait()
	return statuses
}
