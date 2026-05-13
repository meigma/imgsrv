// Package jobs provides a small process-local loop for background jobs.
package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"
)

const (
	// defaultInterval is the idle polling delay used when Config.Interval is unset.
	defaultInterval = 5 * time.Second
	// defaultErrorBackoffMax caps the retry delay used when Config.ErrorBackoffMax is unset.
	defaultErrorBackoffMax = time.Minute
	// errorBackoffFactor is the multiplier applied to the retry delay after each failed attempt.
	errorBackoffFactor = 2
)

// Handler executes one background job attempt for a worker.
type Handler interface {
	// RunOnce attempts one unit of work for workerID.
	RunOnce(context.Context, string) (Result, error)
}

// Result describes one handler attempt.
type Result struct {
	// Worked is true when the handler completed a unit of work.
	Worked bool

	// Attrs are safe structured attributes describing the attempted work unit.
	Attrs []slog.Attr
}

// Config configures a Runner.
type Config struct {
	// Handler executes one background job attempt.
	Handler Handler

	// WorkerID identifies this runner in durable job state.
	WorkerID string

	// Interval is the idle polling delay. Zero selects a conservative default.
	Interval time.Duration

	// ErrorBackoffInitial is the first delay after a failed attempt. Zero uses Interval.
	ErrorBackoffInitial time.Duration

	// ErrorBackoffMax is the maximum delay after repeated failures.
	ErrorBackoffMax time.Duration

	// CircuitBreakerFailures opens the circuit after this many consecutive failures. Zero disables it.
	CircuitBreakerFailures int

	// CircuitBreakerCooldown is the delay after opening the circuit. Zero uses ErrorBackoffMax.
	CircuitBreakerCooldown time.Duration

	// Logger receives background job logs. Nil selects a discarded logger.
	Logger *slog.Logger
}

// Runner repeatedly executes a background job handler.
type Runner struct {
	// handler executes one background job attempt per iteration.
	handler Handler
	// workerID identifies this runner in durable job state.
	workerID string
	// interval is the idle polling delay between attempts that found no work.
	interval time.Duration
	// backoff drives the retry delay after a failed attempt.
	backoff errorBackoff
	// circuitBreaker pauses the loop after repeated consecutive failures.
	circuitBreaker circuitBreaker
	// logger receives background job logs.
	logger *slog.Logger
}

// New constructs a Runner from config.
func New(config Config) *Runner {
	interval := config.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	backoff := newErrorBackoff(interval, config.ErrorBackoffInitial, config.ErrorBackoffMax)
	breaker := newCircuitBreaker(
		config.CircuitBreakerFailures,
		config.CircuitBreakerCooldown,
		backoff.max,
	)
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Runner{
		handler:        config.Handler,
		workerID:       config.WorkerID,
		interval:       interval,
		backoff:        backoff,
		circuitBreaker: breaker,
		logger:         logger,
	}
}

// RunOnce executes exactly one background job attempt.
func (runner *Runner) RunOnce(ctx context.Context) (Result, error) {
	handler, workerID, err := runner.dependencies()
	if err != nil {
		return Result{}, err
	}

	return handler.RunOnce(ctx, workerID)
}

// Run executes the background job loop until ctx is canceled.
func (runner *Runner) Run(ctx context.Context) error {
	handler, workerID, err := runner.dependencies()
	if err != nil {
		return err
	}

	failures := 0
	errorDelay := runner.backoff.initial
	for {
		if canceled(ctx) {
			return nil
		}

		result, err := handler.RunOnce(ctx, workerID)
		attrs := result.logAttrs(workerID)
		if canceled(ctx) {
			return nil
		}
		if err != nil {
			failures++
			delay := errorDelay
			errorDelay = nextBackoff(errorDelay, runner.backoff.max)
			if runner.circuitBreaker.open(failures) {
				delay = runner.circuitBreaker.cooldown
				errorDelay = runner.backoff.initial
				breakerAttrs := appendLogAttrs(
					attrs,
					slog.Int("consecutive_failures", failures),
					slog.Duration("cooldown", delay),
				)
				runner.logger.LogAttrs(
					ctx,
					slog.LevelWarn,
					"background job circuit breaker open",
					breakerAttrs...,
				)
				failures = 0
			}
			errorAttrs := appendLogAttrs(
				attrs,
				slog.Any("error", err),
				slog.Duration("retry_after", delay),
			)
			runner.logger.LogAttrs(
				ctx,
				slog.LevelError,
				"background job attempt failed",
				errorAttrs...,
			)
			if !sleep(ctx, delay) {
				return nil
			}
			continue
		}
		failures = 0
		errorDelay = runner.backoff.initial
		if result.Worked {
			runner.logger.LogAttrs(ctx, slog.LevelDebug, "background job completed work", attrs...)
			continue
		}

		if !sleep(ctx, runner.interval) {
			return nil
		}
	}
}

func (result Result) logAttrs(workerID string) []slog.Attr {
	attrs := []slog.Attr{slog.String("worker_id", workerID)}
	attrs = append(attrs, result.Attrs...)

	return attrs
}

func appendLogAttrs(attrs []slog.Attr, extra ...slog.Attr) []slog.Attr {
	result := make([]slog.Attr, 0, len(attrs)+len(extra))
	result = append(result, attrs...)
	result = append(result, extra...)

	return result
}

// canceled reports whether ctx has been canceled.
func canceled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// dependencies returns the configured handler and worker ID or an error when the runner is not usable.
func (runner *Runner) dependencies() (Handler, string, error) {
	if runner == nil {
		return nil, "", errors.New("job runner is not configured")
	}
	if runner.handler == nil {
		return nil, "", errors.New("job handler is required")
	}
	if strings.TrimSpace(runner.workerID) == "" {
		return nil, "", errors.New("worker id is required")
	}

	return runner.handler, runner.workerID, nil
}

// errorBackoff captures the initial and maximum retry delays after a failed attempt.
type errorBackoff struct {
	initial time.Duration
	max     time.Duration
}

// newErrorBackoff builds an errorBackoff, falling back to interval and the package default when values are unset.
func newErrorBackoff(interval time.Duration, initial time.Duration, maxDelay time.Duration) errorBackoff {
	if initial <= 0 {
		initial = interval
	}
	if maxDelay <= 0 {
		maxDelay = defaultErrorBackoffMax
	}
	if maxDelay < initial {
		maxDelay = initial
	}

	return errorBackoff{
		initial: initial,
		max:     maxDelay,
	}
}

// nextBackoff doubles current up to maxDelay, guarding against overflow.
func nextBackoff(current time.Duration, maxDelay time.Duration) time.Duration {
	next := current * errorBackoffFactor
	if next < current || next > maxDelay {
		return maxDelay
	}

	return next
}

// circuitBreaker pauses the loop for cooldown after failures consecutive errors.
type circuitBreaker struct {
	failures int
	cooldown time.Duration
}

// newCircuitBreaker builds a circuitBreaker, using fallbackCooldown when cooldown is unset.
func newCircuitBreaker(failures int, cooldown time.Duration, fallbackCooldown time.Duration) circuitBreaker {
	if cooldown <= 0 {
		cooldown = fallbackCooldown
	}

	return circuitBreaker{
		failures: failures,
		cooldown: cooldown,
	}
}

// open reports whether the breaker should trip given the current consecutive failure count.
func (breaker circuitBreaker) open(failures int) bool {
	return breaker.failures > 0 && failures >= breaker.failures
}

// sleep waits for delay or until ctx is canceled. It returns true when the delay elapsed.
func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
