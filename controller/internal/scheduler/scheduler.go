package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/shiva-load-testing/controller/internal/model"
	"github.com/shiva-load-testing/controller/internal/secrets"
	"github.com/shiva-load-testing/controller/internal/store"
)

const (
	tickInterval          = 10 * time.Second
	consecutiveFailLimit  = 3
	maxRecoveryAdvances   = 1000
)

// TestExecutor is the interface the scheduler uses to run tests.
// Implemented by handler.TestHandler.
type TestExecutor interface {
	ExecuteTest(ctx context.Context, req model.TestRequest, userID int64, username string) (testID string, controllable bool, warnings []model.ConflictWarning, err error)
}

// ActiveTestProvider provides info about the currently running test.
type ActiveTestProvider interface {
	GetActiveTestID() string
	GetTestStartTime() time.Time
}

// Scheduler runs a background loop that checks for due scheduled tests and executes them.
// It is database-backed and survives controller restarts.
type Scheduler struct {
	store    *store.Store
	executor TestExecutor
	active   ActiveTestProvider
	logger   *slog.Logger
	secretSvc *secrets.Service

	mu     sync.Mutex
	stopCh chan struct{}

	// onComplete callback registry: executionID → completion handler
	callbacks   map[string]string // executionID → scheduleID
	callbacksMu sync.Mutex
}

type scheduledExecutionStart struct {
	execID    string
	scheduleID string
	startedAt time.Time
}

type scheduledExecutionCompletion struct {
	execID     string
	scheduleID string
	testID     string
	status     string
	errMsg     string
	finishedAt time.Time
}

func New(st *store.Store, exec TestExecutor, active ActiveTestProvider, logger *slog.Logger, encryptionKey string) *Scheduler {
	var secretSvc *secrets.Service
	if encryptionKey != "" {
		if svc, err := secrets.NewService(encryptionKey); err == nil {
			secretSvc = svc
		}
	}
	return &Scheduler{
		store:     st,
		executor:  exec,
		active:    active,
		logger:    logger,
		secretSvc: secretSvc,
		callbacks: make(map[string]string),
	}
}

func (s *Scheduler) registerCallback(execID, scheduleID string) {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()
	s.callbacks[execID] = scheduleID
}

func (s *Scheduler) clearCallback(execID string) {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()
	delete(s.callbacks, execID)
}

func (s *Scheduler) takeNextCallback() (string, string) {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()

	for execID, scheduleID := range s.callbacks {
		delete(s.callbacks, execID)
		return execID, scheduleID
	}
	return "", ""
}

func (s *Scheduler) hasActiveTest() bool {
	return s.active.GetActiveTestID() != ""
}

func (s *Scheduler) nextDueSchedule(ctx context.Context) (*model.ScheduledTest, error) {
	return s.store.GetDueSchedule(ctx)
}

func (s *Scheduler) markScheduleRunning(ctx context.Context, scheduleID string) error {
	return s.store.UpdateScheduleStatus(ctx, scheduleID, "running")
}

func (s *Scheduler) createRunningExecution(ctx context.Context, schedule *model.ScheduledTest) (string, time.Time, error) {
	execID := uuid.New().String()
	now := time.Now()
	exec := &model.ScheduleExecution{
		ID:          execID,
		ScheduleID:  schedule.ID,
		Status:      "running",
		ScheduledAt: schedule.ScheduledAt,
		StartedAt:   &now,
	}
	if err := s.store.CreateExecution(ctx, exec); err != nil {
		return "", time.Time{}, err
	}
	return execID, now, nil
}

func (s *Scheduler) failExecution(ctx context.Context, execID string, startedAt, endedAt *time.Time, errMsg *string) error {
	return s.store.UpdateExecution(ctx, execID, "failed", nil, startedAt, endedAt, errMsg, nil)
}

func (s *Scheduler) markExecutionRunning(ctx context.Context, execID, testID string, startedAt time.Time) error {
	return s.store.UpdateExecution(ctx, execID, "running", &testID, &startedAt, nil, nil, nil)
}

func (s *Scheduler) completeExecution(ctx context.Context, execID, testID, status, errMsg string, finishedAt time.Time) error {
	execStatus := "completed"
	var errMsgPtr *string
	if status != "completed" {
		execStatus = "failed"
		errMsgPtr = &errMsg
	}
	return s.store.UpdateExecution(ctx, execID, execStatus, &testID, nil, &finishedAt, errMsgPtr, nil)
}

func (s *Scheduler) resetScheduleToScheduled(ctx context.Context, scheduleID string) {
	_ = s.store.UpdateScheduleStatus(ctx, scheduleID, "scheduled")
}

func (s *Scheduler) beginScheduledExecution(ctx context.Context, schedule *model.ScheduledTest) (*scheduledExecutionStart, error) {
	if err := s.markScheduleRunning(ctx, schedule.ID); err != nil {
		return nil, err
	}

	execID, now, err := s.createRunningExecution(ctx, schedule)
	if err != nil {
		s.resetScheduleToScheduled(ctx, schedule.ID)
		return nil, err
	}

	s.registerCallback(execID, schedule.ID)
	return &scheduledExecutionStart{
		execID:     execID,
		scheduleID: schedule.ID,
		startedAt:  now,
	}, nil
}

func (s *Scheduler) failScheduledExecution(ctx context.Context, start *scheduledExecutionStart, err error) {
	s.clearCallback(start.execID)

	errStr := err.Error()
	if updateErr := s.failExecution(ctx, start.execID, &start.startedAt, &start.startedAt, &errStr); updateErr != nil {
		s.logger.Error("scheduler: failed to update execution on error", "error", updateErr)
	}

	s.advanceSchedule(ctx, start.scheduleID, true)
}

func (s *Scheduler) nextCompletion(testID string, status string, errMsg string) *scheduledExecutionCompletion {
	execID, schedID := s.takeNextCallback()
	if execID == "" {
		return nil
	}

	return &scheduledExecutionCompletion{
		execID:     execID,
		scheduleID: schedID,
		testID:     testID,
		status:     status,
		errMsg:     errMsg,
		finishedAt: time.Now(),
	}
}

func (s *Scheduler) applyCompletion(ctx context.Context, completion *scheduledExecutionCompletion) {
	if err := s.completeExecution(ctx, completion.execID, completion.testID, completion.status, completion.errMsg, completion.finishedAt); err != nil {
		s.logger.Error("failed to update execution on complete", "error", err, "execution_id", completion.execID)
	}

	s.advanceSchedule(ctx, completion.scheduleID, completion.status != "completed")
}

func (s *Scheduler) recoverStaleExecutions(ctx context.Context) {
	if affected, err := s.store.MarkStaleScheduleExecutions(ctx); err != nil {
		s.logger.Error("scheduler: failed to mark stale executions", "error", err)
	} else if affected > 0 {
		s.logger.Info("scheduler: cleaned up stale executions", "count", affected)
	}
}

func (s *Scheduler) recoverStaleSchedules(ctx context.Context) {
	if affected, err := s.store.ResetStaleRunningSchedules(ctx); err != nil {
		s.logger.Error("scheduler: failed to reset stale schedules", "error", err)
	} else if affected > 0 {
		s.logger.Info("scheduler: reset stale running schedules", "count", affected)
	}
}

func (s *Scheduler) shouldNormalizeOverdueSchedule(schedule *model.ScheduledTest, recoveryAt time.Time) bool {
	return schedule.Status == "scheduled" && !schedule.Paused && schedule.ScheduledAt.Before(recoveryAt)
}

func (s *Scheduler) nextFutureOccurrence(schedule *model.ScheduledTest, recoveryAt time.Time) (time.Time, bool, error) {
	current := schedule.ScheduledAt

	for i := 0; i < maxRecoveryAdvances; i++ {
		nextAt, err := NextOccurrence(current, schedule.RecurrenceType, schedule.Timezone, schedule.RecurrenceEnd)
		if err != nil {
			return time.Time{}, false, err
		}
		if nextAt.IsZero() {
			return time.Time{}, true, nil
		}
		if !nextAt.Before(recoveryAt) {
			return nextAt, false, nil
		}
		current = nextAt
	}

	return time.Time{}, false, fmt.Errorf("schedule %s exceeded recovery advancement limit", schedule.ID)
}

func (s *Scheduler) normalizeOverdueSchedule(ctx context.Context, schedule *model.ScheduledTest, recoveryAt time.Time) bool {
	if schedule.RecurrenceType == "once" {
		if err := s.store.UpdateScheduleStatus(ctx, schedule.ID, "failed"); err != nil {
			s.logger.Error("scheduler: failed to mark overdue one-time schedule", "error", err, "schedule_id", schedule.ID)
			return false
		}

		s.logger.Info("scheduler: skipped overdue one-time schedule on startup",
			"schedule_id", schedule.ID,
			"name", schedule.Name,
			"scheduled_at", schedule.ScheduledAt,
		)
		return true
	}

	nextAt, exhausted, err := s.nextFutureOccurrence(schedule, recoveryAt)
	if err != nil {
		s.logger.Error("scheduler: failed to advance overdue recurring schedule", "error", err, "schedule_id", schedule.ID)
		if updateErr := s.store.UpdateScheduleStatus(ctx, schedule.ID, "failed"); updateErr != nil {
			s.logger.Error("scheduler: failed to mark overdue recurring schedule as failed", "error", updateErr, "schedule_id", schedule.ID)
		}
		return false
	}

	if exhausted {
		if err := s.store.UpdateScheduleStatus(ctx, schedule.ID, "completed"); err != nil {
			s.logger.Error("scheduler: failed to complete expired recurring schedule", "error", err, "schedule_id", schedule.ID)
			return false
		}

		s.logger.Info("scheduler: completed expired recurring schedule during startup recovery",
			"schedule_id", schedule.ID,
			"name", schedule.Name,
			"scheduled_at", schedule.ScheduledAt,
		)
		return true
	}

	if err := s.store.UpdateScheduleNextRun(ctx, schedule.ID, nextAt, "scheduled"); err != nil {
		s.logger.Error("scheduler: failed to reschedule overdue recurring schedule", "error", err, "schedule_id", schedule.ID)
		return false
	}

	s.logger.Info("scheduler: advanced overdue recurring schedule during startup recovery",
		"schedule_id", schedule.ID,
		"name", schedule.Name,
		"from", schedule.ScheduledAt,
		"next_at", nextAt,
	)
	return true
}

func (s *Scheduler) recoverOverdueSchedules(ctx context.Context) {
	schedules, err := s.store.ListSchedules(ctx)
	if err != nil {
		s.logger.Error("scheduler: failed to list schedules for overdue recovery", "error", err)
		return
	}

	recoveryAt := time.Now().UTC()
	normalized := 0
	for i := range schedules {
		schedule := &schedules[i]
		if !s.shouldNormalizeOverdueSchedule(schedule, recoveryAt) {
			continue
		}
		if s.normalizeOverdueSchedule(ctx, schedule, recoveryAt) {
			normalized++
		}
	}

	if normalized > 0 {
		s.logger.Info("scheduler: normalized overdue schedules during startup recovery", "count", normalized)
	}
}

// Start begins the scheduler tick loop. Call Stop() to shut down.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.stopCh != nil {
		s.mu.Unlock()
		return // already running
	}
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	// Startup recovery: clean up stale state from previous crash
	s.recover(ctx)

	go s.run()
	s.logger.Info("scheduler started", "tick_interval", tickInterval)
}

// Stop shuts down the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh = nil
	}
}

// OnTestComplete should be called by the test handler when a scheduled test finishes.
// It updates the execution record and advances recurring schedules.
func (s *Scheduler) OnTestComplete(testID string, status string, errMsg string) {
	completion := s.nextCompletion(testID, status, errMsg)
	if completion == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s.applyCompletion(ctx, completion)
}

// run is the main tick loop.
func (s *Scheduler) run() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Scheduler) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check if a test is currently running
	if s.hasActiveTest() {
		return // Can't start a new test while one is running
	}

	schedule, err := s.nextDueSchedule(ctx)
	if err != nil {
		s.logger.Error("scheduler: failed to get due schedule", "error", err)
		return
	}
	if schedule == nil {
		return // Nothing due
	}

	s.logger.Info("scheduler: executing due test",
		"schedule_id", schedule.ID,
		"name", schedule.Name,
		"scheduled_at", schedule.ScheduledAt,
	)

	s.executeSchedule(ctx, schedule)
}

func (s *Scheduler) executeSchedule(ctx context.Context, schedule *model.ScheduledTest) {
	start, err := s.beginScheduledExecution(ctx, schedule)
	if err != nil {
		s.logger.Error("scheduler: failed to begin scheduled execution", "error", err)
		return
	}

	req, err := s.scheduleTestRequest(schedule)
	if err != nil {
		s.logger.Error("scheduler: failed to prepare scheduled auth config",
			"error", err,
			"schedule_id", schedule.ID,
			"name", schedule.Name,
		)
		s.failScheduledExecution(ctx, start, err)
		return
	}
	testID, _, _, err := s.executor.ExecuteTest(ctx, req, schedule.UserID, schedule.Username)
	if err != nil {
		s.logger.Error("scheduler: test execution failed",
			"error", err,
			"schedule_id", schedule.ID,
			"name", schedule.Name,
		)

		s.failScheduledExecution(ctx, start, err)
		return
	}

	if err := s.markExecutionRunning(ctx, start.execID, testID, start.startedAt); err != nil {
		s.logger.Error("scheduler: failed to update execution with test ID", "error", err)
	}

	s.logger.Info("scheduler: test started successfully",
		"schedule_id", schedule.ID,
		"test_id", testID,
		"name", schedule.Name,
	)
}

func (s *Scheduler) scheduleTestRequest(schedule *model.ScheduledTest) (model.TestRequest, error) {
	req := schedule.ToTestRequest()
	if !schedule.AuthConfig.Enabled {
		return req, nil
	}
	if schedule.AuthConfig.ClientSecretEncrypted == "" {
		return model.TestRequest{}, fmt.Errorf("scheduled auth secret is missing")
	}
	if s.secretSvc == nil {
		return model.TestRequest{}, fmt.Errorf("scheduler auth secret is configured but encryption service is unavailable")
	}

	clientSecret, err := s.secretSvc.Decrypt(schedule.AuthConfig.ClientSecretEncrypted)
	if err != nil {
		return model.TestRequest{}, fmt.Errorf("failed to decrypt scheduled auth secret: %w", err)
	}
	req.Auth.ClientSecret = clientSecret
	return req, nil
}

// advanceSchedule moves a recurring schedule to its next occurrence,
// or marks a one-time schedule as completed/failed.
func (s *Scheduler) advanceSchedule(ctx context.Context, scheduleID string, failed bool) {
	schedule, err := s.store.GetSchedule(ctx, scheduleID)
	if err != nil || schedule == nil {
		s.logger.Error("scheduler: failed to get schedule for advancing", "error", err, "id", scheduleID)
		return
	}

	if schedule.RecurrenceType == "once" {
		status := "completed"
		if failed {
			status = "failed"
		}
		_ = s.store.UpdateScheduleStatus(ctx, scheduleID, status)
		return
	}

	// Auto-pause after consecutive failures
	if failed {
		failCount, err := s.store.CountConsecutiveFailures(ctx, scheduleID)
		if err == nil && failCount >= consecutiveFailLimit {
			s.logger.Warn("scheduler: auto-pausing schedule after consecutive failures",
				"schedule_id", scheduleID,
				"name", schedule.Name,
				"consecutive_failures", failCount,
			)
			_ = s.store.PauseSchedule(ctx, scheduleID, true)
			_ = s.store.UpdateScheduleStatus(ctx, scheduleID, "scheduled")
			return
		}
	}

	// Calculate next occurrence
	nextAt, err := NextOccurrence(schedule.ScheduledAt, schedule.RecurrenceType, schedule.Timezone, schedule.RecurrenceEnd)
	if err != nil {
		s.logger.Error("scheduler: failed to calculate next occurrence", "error", err)
		_ = s.store.UpdateScheduleStatus(ctx, scheduleID, "failed")
		return
	}

	if nextAt.IsZero() {
		// No more occurrences (past end date)
		_ = s.store.UpdateScheduleStatus(ctx, scheduleID, "completed")
		return
	}

	_ = s.store.UpdateScheduleNextRun(ctx, scheduleID, nextAt, "scheduled")
	s.logger.Info("scheduler: advanced to next occurrence",
		"schedule_id", scheduleID,
		"next_at", nextAt,
	)
}

// recover cleans up stale state from a previous crash.
func (s *Scheduler) recover(ctx context.Context) {
	s.recoverStaleExecutions(ctx)
	s.recoverStaleSchedules(ctx)
	s.recoverOverdueSchedules(ctx)
}
