package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
)

// RampingManager implements controller-side VU ramping for the
// externally-controlled k6 executor. It walks through stages and
// sends ScaleVUs commands at regular intervals to smoothly ramp
// VUs up or down. Supports pause/resume with timer preservation.
type RampingManager struct {
	orch   *Orchestrator
	logger *slog.Logger

	mu        sync.Mutex
	stages    []model.Stage
	cancel    context.CancelFunc
	paused    bool
	elapsed   time.Duration // how far into the current run we are
	startTime time.Time
	done      chan struct{}

	// Manual override: when the user scales VUs via API, the override
	// is held until the next stage begins.
	manualVUs        int // 0 = no override
	overrideStageIdx int // stage index when override was set
}

func NewRampingManager(orch *Orchestrator, logger *slog.Logger) *RampingManager {
	return &RampingManager{
		orch:             orch,
		logger:           logger,
		overrideStageIdx: -1,
	}
}

// Start begins executing the stage schedule in a background goroutine.
func (rm *RampingManager) Start(stages []model.Stage) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.cancel != nil {
		rm.cancel()
	}

	rm.stages = stages
	rm.elapsed = 0
	rm.paused = false
	rm.done = make(chan struct{})
	rm.manualVUs = 0
	rm.overrideStageIdx = -1

	ctx, cancel := context.WithCancel(context.Background())
	rm.cancel = cancel
	rm.startTime = time.Now()

	go rm.run(ctx)
}

// SetManualOverride records a manual VU adjustment from the user.
// The override persists until the next stage boundary.
func (rm *RampingManager) SetManualOverride(vus int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if len(rm.stages) == 0 {
		return // no stages — direct k6 control, no override needed
	}

	elapsed := rm.elapsed
	if !rm.paused {
		elapsed += time.Since(rm.startTime)
	}

	_, _, stageIdx := calculateVUs(rm.stages, elapsed)

	rm.manualVUs = vus
	rm.overrideStageIdx = stageIdx
	rm.logger.Info("manual VU override set",
		"vus", vus,
		"stage_index", stageIdx,
	)
}

// Pause stops VU ramping and pauses all k6 workers.
// Works even if no ramping stages are active (direct k6 pause).
func (rm *RampingManager) Pause(ctx context.Context) error {
	rm.mu.Lock()
	if rm.paused {
		rm.mu.Unlock()
		return nil
	}
	hasStages := len(rm.stages) > 0
	if hasStages {
		// Record how much time has passed before pausing
		rm.elapsed += time.Since(rm.startTime)
	}
	rm.paused = true
	// Cancel the current run loop so it stops ticking
	if rm.cancel != nil {
		rm.cancel()
	}
	rm.mu.Unlock()

	// Pause k6 workers
	return rm.orch.PauseAll(ctx)
}

// Resume resumes k6 workers and continues VU ramping from where it left off.
// Works even if no ramping stages are active (direct k6 resume).
func (rm *RampingManager) Resume(ctx context.Context) error {
	// Resume k6 workers first
	if err := rm.orch.ResumeAll(ctx); err != nil {
		return err
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	if !rm.paused {
		return nil
	}

	rm.paused = false

	// Only restart the ramping loop if we have stages
	if len(rm.stages) > 0 {
		rm.startTime = time.Now()
		rm.done = make(chan struct{})

		newCtx, cancel := context.WithCancel(context.Background())
		rm.cancel = cancel

		go rm.run(newCtx)
	}
	return nil
}

// Stop cancels the ramping manager.
func (rm *RampingManager) Stop() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.cancel != nil {
		rm.cancel()
		rm.cancel = nil
	}
}

// IsPaused returns whether the ramping manager is currently paused.
func (rm *RampingManager) IsPaused() bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.paused
}

// Done returns a channel that's closed when all stages complete.
func (rm *RampingManager) Done() <-chan struct{} {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.done == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return rm.done
}

// run is the main loop that executes the stage schedule.
// It calculates the target VU count at each tick based on elapsed time
// and sends scale commands to the orchestrator.
func (rm *RampingManager) run(ctx context.Context) {
	defer func() {
		rm.mu.Lock()
		if rm.done != nil {
			select {
			case <-rm.done:
			default:
				close(rm.done)
			}
		}
		rm.mu.Unlock()
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rm.mu.Lock()
			elapsed := rm.elapsed + time.Since(rm.startTime)
			stages := rm.stages
			manualVUs := rm.manualVUs
			overrideIdx := rm.overrideStageIdx
			rm.mu.Unlock()

			targetVUs, allDone, stageIdx := calculateVUs(stages, elapsed)

			if allDone {
				rm.logger.Info("all ramping stages completed")
				// Signal the poll loop that ramping is done — it will trigger onTestComplete
				rm.orch.SetRampingDone()
				return
			}

			// Apply manual override if active and still in the same stage
			effectiveVUs := targetVUs
			if manualVUs > 0 && stageIdx == overrideIdx {
				effectiveVUs = manualVUs
			} else if manualVUs > 0 && stageIdx != overrideIdx {
				// Entered a new stage — clear the override
				rm.mu.Lock()
				rm.manualVUs = 0
				rm.overrideStageIdx = -1
				rm.mu.Unlock()
				rm.logger.Info("manual VU override cleared (new stage reached)",
					"stage_index", stageIdx,
					"scheduled_vus", targetVUs,
				)
			}

			// Scale to target (ScaleVUs distributes across workers)
			if err := rm.orch.ScaleVUs(ctx, effectiveVUs); err != nil {
				// Don't log on context cancelled (normal during pause/stop)
				if ctx.Err() == nil {
					rm.logger.Warn("ramping scale failed", "error", err)
				}
				return
			}
		}
	}
}

// calculateVUs determines the target VU count at a given elapsed time.
// It linearly interpolates between stage targets and returns the current stage index.
func calculateVUs(stages []model.Stage, elapsed time.Duration) (targetVUs int, allDone bool, stageIdx int) {
	if len(stages) == 0 {
		return 0, true, -1
	}

	var cumulative time.Duration
	prevTarget := 0

	for i, stage := range stages {
		stageDur := parseStageDuration(stage.Duration)
		stageEnd := cumulative + stageDur

		if elapsed < stageEnd {
			// We're within this stage — linearly interpolate
			intoStage := elapsed - cumulative
			progress := float64(intoStage) / float64(stageDur)
			if progress < 0 {
				progress = 0
			}
			if progress > 1 {
				progress = 1
			}
			vus := float64(prevTarget) + progress*float64(stage.Target-prevTarget)
			return int(vus), false, i
		}

		cumulative = stageEnd
		prevTarget = stage.Target
	}

	// Past all stages
	return prevTarget, true, len(stages) - 1
}

// parseStageDuration converts "30s", "2m", "1h" to time.Duration.
func parseStageDuration(d string) time.Duration {
	d = strings.TrimSpace(d)
	if d == "" {
		return 0
	}
	unit := d[len(d)-1]
	val := 0
	fmt.Sscanf(d[:len(d)-1], "%d", &val)
	switch unit {
	case 's':
		return time.Duration(val) * time.Second
	case 'm':
		return time.Duration(val) * time.Minute
	case 'h':
		return time.Duration(val) * time.Hour
	default:
		return time.Duration(val) * time.Second
	}
}
