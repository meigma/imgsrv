// Package metrics defines optional application metrics for imgsrv.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// PostgresObserver provides Postgres pool and durable service-state snapshots.
type PostgresObserver interface {
	// PoolMetrics returns a current snapshot of the Postgres connection pool.
	PoolMetrics() (PostgresPoolSnapshot, error)

	// StoreMetrics returns a current snapshot of durable imgsrv state.
	StoreMetrics(context.Context) (StoreSnapshot, error)
}

// PostgresPoolSnapshot contains current and cumulative pgx pool statistics.
type PostgresPoolSnapshot struct {
	// AcquiredConns is the number of connections currently checked out.
	AcquiredConns int64

	// IdleConns is the number of currently idle connections.
	IdleConns int64

	// ConstructingConns is the number of connections currently being opened.
	ConstructingConns int64

	// TotalConns is the total number of opened or opening pool connections.
	TotalConns int64

	// MaxConns is the configured maximum pool size.
	MaxConns int64

	// AcquireCount is the cumulative count of successful pool acquisitions.
	AcquireCount int64

	// EmptyAcquireCount is the cumulative count of acquisitions that had to wait.
	EmptyAcquireCount int64

	// CanceledAcquireCount is the cumulative count of canceled acquisitions.
	CanceledAcquireCount int64

	// AcquireDuration is the cumulative duration spent acquiring connections.
	AcquireDuration time.Duration
}

// StateCount is a count grouped by durable state.
type StateCount struct {
	// State is the bounded durable state label.
	State string

	// Count is the number of rows currently in State.
	Count int64
}

// StepStateCount is a count grouped by publish step and durable state.
type StepStateCount struct {
	// Step is the bounded publish step label.
	Step string

	// State is the bounded durable state label.
	State string

	// Count is the number of rows currently matching Step and State.
	Count int64
}

// StepAge is an age measurement grouped by publish step.
type StepAge struct {
	// Step is the bounded publish step label.
	Step string

	// Age is how long the oldest matching row has been waiting or running.
	Age time.Duration
}

// StoreSnapshot contains durable service-state metrics observed from Postgres.
type StoreSnapshot struct {
	// UploadSessions counts upload sessions by state.
	UploadSessions []StateCount

	// CASIngestJobs counts CAS ingest jobs by state.
	CASIngestJobs []StateCount

	// CASIngestOldestQueuedAge is the age of the oldest due queued CAS ingest job.
	CASIngestOldestQueuedAge time.Duration

	// HasCASIngestOldestQueuedAge reports whether CASIngestOldestQueuedAge is populated.
	HasCASIngestOldestQueuedAge bool

	// CASIngestOldestRunningAge is the age of the oldest running CAS ingest job.
	CASIngestOldestRunningAge time.Duration

	// HasCASIngestOldestRunningAge reports whether CASIngestOldestRunningAge is populated.
	HasCASIngestOldestRunningAge bool

	// CASBlobs is the current number of verified CAS blobs.
	CASBlobs int64

	// CASBlobBytes is the current total byte size of verified CAS blobs.
	CASBlobBytes int64

	// PublishJobs counts publish jobs by state.
	PublishJobs []StateCount

	// PublishSteps counts publish steps by step and state.
	PublishSteps []StepStateCount

	// PublishStepOldestQueuedAges contains the oldest due queued step age per step.
	PublishStepOldestQueuedAges []StepAge

	// PublishStepOldestRunningAges contains the oldest running step age per step.
	PublishStepOldestRunningAges []StepAge

	// PublishingVersions is the current number of versions in publishing state.
	PublishingVersions int64

	// IncusProjectionRows is the current number of Incus projection rows.
	IncusProjectionRows int64
}

// Recorder records optional imgsrv application metrics.
type Recorder struct {
	enabled bool
	meter   metric.Meter

	objectstoreOperations       metric.Int64Counter
	objectstoreOperationLatency metric.Float64Histogram
	objectstoreBytes            metric.Int64Counter

	backgroundJobAttempts            metric.Int64Counter
	backgroundJobErrors              metric.Int64Counter
	backgroundJobCircuitOpen         metric.Int64Counter
	backgroundJobConsecutiveFailures metric.Int64Gauge
	backgroundJobLastSuccess         metric.Float64Gauge
	backgroundJobLastError           metric.Float64Gauge

	postgresPoolConnections      metric.Int64ObservableGauge
	postgresPoolAcquires         metric.Int64ObservableCounter
	postgresPoolEmptyAcquires    metric.Int64ObservableCounter
	postgresPoolCanceledAcquires metric.Int64ObservableCounter
	postgresPoolAcquireDuration  metric.Float64ObservableCounter

	uploadSessions               metric.Int64ObservableGauge
	casIngestJobs                metric.Int64ObservableGauge
	casIngestOldestQueuedAge     metric.Float64ObservableGauge
	casIngestOldestRunningAge    metric.Float64ObservableGauge
	casBlobs                     metric.Int64ObservableGauge
	casBlobBytes                 metric.Int64ObservableGauge
	publishJobs                  metric.Int64ObservableGauge
	publishSteps                 metric.Int64ObservableGauge
	publishStepOldestQueuedAges  metric.Float64ObservableGauge
	publishStepOldestRunningAges metric.Float64ObservableGauge
	publishingVersions           metric.Int64ObservableGauge
	incusProjectionRows          metric.Int64ObservableGauge

	mu                 sync.Mutex
	postgresRegistered bool
}

// Noop returns a disabled Recorder that drops all observations.
func Noop() *Recorder {
	return &Recorder{}
}

// New constructs an OTel-backed Recorder from meter.
func New(meter metric.Meter) (*Recorder, error) {
	if meter == nil {
		return Noop(), nil
	}

	recorder := &Recorder{
		enabled: true,
		meter:   meter,
	}
	if err := recorder.initInstruments(); err != nil {
		return nil, err
	}

	return recorder, nil
}

// Enabled reports whether recorder emits metrics.
func (recorder *Recorder) Enabled() bool {
	return recorder != nil && recorder.enabled
}

// RegisterPostgres registers scrape-time Postgres pool and durable state observers.
func (recorder *Recorder) RegisterPostgres(provider PostgresObserver) error {
	if !recorder.Enabled() || provider == nil {
		return nil
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.postgresRegistered {
		return nil
	}

	_, err := recorder.meter.RegisterCallback(
		func(ctx context.Context, observer metric.Observer) error {
			recorder.observePostgresPool(observer, provider)
			recorder.observeStoreState(ctx, observer, provider)
			return nil
		},
		recorder.postgresPoolConnections,
		recorder.postgresPoolAcquires,
		recorder.postgresPoolEmptyAcquires,
		recorder.postgresPoolCanceledAcquires,
		recorder.postgresPoolAcquireDuration,
		recorder.uploadSessions,
		recorder.casIngestJobs,
		recorder.casIngestOldestQueuedAge,
		recorder.casIngestOldestRunningAge,
		recorder.casBlobs,
		recorder.casBlobBytes,
		recorder.publishJobs,
		recorder.publishSteps,
		recorder.publishStepOldestQueuedAges,
		recorder.publishStepOldestRunningAges,
		recorder.publishingVersions,
		recorder.incusProjectionRows,
	)
	if err != nil {
		return fmt.Errorf("register postgres metrics: %w", err)
	}
	recorder.postgresRegistered = true

	return nil
}

// RecordObjectstoreOperation records one object-store operation attempt.
func (recorder *Recorder) RecordObjectstoreOperation(
	ctx context.Context,
	operation string,
	outcome string,
	duration time.Duration,
) {
	if !recorder.Enabled() {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("outcome", outcome),
	)
	recorder.objectstoreOperations.Add(ctx, 1, attrs)
	recorder.objectstoreOperationLatency.Record(ctx, duration.Seconds(), attrs)
}

// RecordObjectstoreBytes records known object-store transfer bytes.
func (recorder *Recorder) RecordObjectstoreBytes(
	ctx context.Context,
	operation string,
	direction string,
	sizeBytes int64,
) {
	if !recorder.Enabled() || sizeBytes <= 0 {
		return
	}
	recorder.objectstoreBytes.Add(
		ctx,
		sizeBytes,
		metric.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("direction", direction),
		),
	)
}

// RecordBackgroundJobAttempt records one background job attempt outcome.
func (recorder *Recorder) RecordBackgroundJobAttempt(ctx context.Context, job string, outcome string) {
	if !recorder.Enabled() {
		return
	}
	recorder.backgroundJobAttempts.Add(
		ctx,
		1,
		metric.WithAttributes(attribute.String("job", job), attribute.String("outcome", outcome)),
	)
	if outcome != "error" {
		recorder.backgroundJobLastSuccess.Record(ctx, timestampSeconds(time.Now()), jobAttrs(job))
	}
}

// RecordBackgroundJobError records one background job error.
func (recorder *Recorder) RecordBackgroundJobError(ctx context.Context, job string) {
	if !recorder.Enabled() {
		return
	}
	recorder.backgroundJobErrors.Add(ctx, 1, jobAttrs(job))
	recorder.backgroundJobLastError.Record(ctx, timestampSeconds(time.Now()), jobAttrs(job))
}

// RecordBackgroundJobCircuitOpen records one background job circuit breaker opening.
func (recorder *Recorder) RecordBackgroundJobCircuitOpen(ctx context.Context, job string) {
	if !recorder.Enabled() {
		return
	}
	recorder.backgroundJobCircuitOpen.Add(ctx, 1, jobAttrs(job))
}

// RecordBackgroundJobConsecutiveFailures records the current consecutive failure count.
func (recorder *Recorder) RecordBackgroundJobConsecutiveFailures(ctx context.Context, job string, failures int64) {
	if !recorder.Enabled() {
		return
	}
	recorder.backgroundJobConsecutiveFailures.Record(ctx, failures, jobAttrs(job))
}

func (recorder *Recorder) initInstruments() error {
	var err error
	var errs []error

	recorder.objectstoreOperations, err = recorder.meter.Int64Counter(
		"imgsrv.objectstore.operations",
		metric.WithDescription("Object-store operation attempts."),
		metric.WithUnit("{operation}"),
	)
	errs = append(errs, err)
	recorder.objectstoreOperationLatency, err = recorder.meter.Float64Histogram(
		"imgsrv.objectstore.operation.duration",
		metric.WithDescription("Object-store operation latency."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)
	recorder.objectstoreBytes, err = recorder.meter.Int64Counter(
		"imgsrv.objectstore.bytes",
		metric.WithDescription("Known object-store transfer bytes."),
		metric.WithUnit("By"),
	)
	errs = append(errs, err)

	recorder.backgroundJobAttempts, err = recorder.meter.Int64Counter(
		"imgsrv.background.job.attempts",
		metric.WithDescription("Background job attempts."),
		metric.WithUnit("{attempt}"),
	)
	errs = append(errs, err)
	recorder.backgroundJobErrors, err = recorder.meter.Int64Counter(
		"imgsrv.background.job.errors",
		metric.WithDescription("Background job errors."),
		metric.WithUnit("{error}"),
	)
	errs = append(errs, err)
	recorder.backgroundJobCircuitOpen, err = recorder.meter.Int64Counter(
		"imgsrv.background.job.circuit.open",
		metric.WithDescription("Background job circuit breaker openings."),
		metric.WithUnit("{event}"),
	)
	errs = append(errs, err)
	recorder.backgroundJobConsecutiveFailures, err = recorder.meter.Int64Gauge(
		"imgsrv.background.job.consecutive.failures",
		metric.WithDescription("Current consecutive background job failures."),
		metric.WithUnit("{failure}"),
	)
	errs = append(errs, err)
	recorder.backgroundJobLastSuccess, err = recorder.meter.Float64Gauge(
		"imgsrv.background.job.last.success.timestamp",
		metric.WithDescription("Unix timestamp of the last successful background job attempt."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)
	recorder.backgroundJobLastError, err = recorder.meter.Float64Gauge(
		"imgsrv.background.job.last.error.timestamp",
		metric.WithDescription("Unix timestamp of the last background job error."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)

	errs = append(errs, recorder.initPostgresInstruments())

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("create metrics instruments: %w", err)
	}
	return nil
}

func (recorder *Recorder) initPostgresInstruments() error {
	return errors.Join(
		recorder.initPostgresPoolInstruments(),
		recorder.initStoreStateInstruments(),
	)
}

func (recorder *Recorder) initPostgresPoolInstruments() error {
	var err error
	var errs []error

	recorder.postgresPoolConnections, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.postgres.pool.connections",
		metric.WithDescription("Current Postgres pool connections."),
		metric.WithUnit("{connection}"),
	)
	errs = append(errs, err)
	recorder.postgresPoolAcquires, err = recorder.meter.Int64ObservableCounter(
		"imgsrv.postgres.pool.acquires",
		metric.WithDescription("Successful Postgres pool acquisitions."),
		metric.WithUnit("{acquire}"),
	)
	errs = append(errs, err)
	recorder.postgresPoolEmptyAcquires, err = recorder.meter.Int64ObservableCounter(
		"imgsrv.postgres.pool.empty.acquires",
		metric.WithDescription("Postgres pool acquisitions that waited because the pool was empty."),
		metric.WithUnit("{acquire}"),
	)
	errs = append(errs, err)
	recorder.postgresPoolCanceledAcquires, err = recorder.meter.Int64ObservableCounter(
		"imgsrv.postgres.pool.canceled.acquires",
		metric.WithDescription("Postgres pool acquisitions canceled by context."),
		metric.WithUnit("{acquire}"),
	)
	errs = append(errs, err)
	recorder.postgresPoolAcquireDuration, err = recorder.meter.Float64ObservableCounter(
		"imgsrv.postgres.pool.acquire.duration",
		metric.WithDescription("Cumulative Postgres pool acquisition duration."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)

	return errors.Join(errs...)
}

func (recorder *Recorder) initStoreStateInstruments() error {
	var err error
	var errs []error

	recorder.uploadSessions, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.upload.sessions",
		metric.WithDescription("Upload sessions by durable state."),
		metric.WithUnit("{session}"),
	)
	errs = append(errs, err)
	recorder.casIngestJobs, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.cas.ingest.jobs",
		metric.WithDescription("CAS ingest jobs by durable state."),
		metric.WithUnit("{job}"),
	)
	errs = append(errs, err)
	recorder.casIngestOldestQueuedAge, err = recorder.meter.Float64ObservableGauge(
		"imgsrv.cas.ingest.oldest.queued.age",
		metric.WithDescription("Age of the oldest due queued CAS ingest job."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)
	recorder.casIngestOldestRunningAge, err = recorder.meter.Float64ObservableGauge(
		"imgsrv.cas.ingest.oldest.running.age",
		metric.WithDescription("Age of the oldest running CAS ingest job."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)
	recorder.casBlobs, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.cas.blobs",
		metric.WithDescription("Verified CAS blob count."),
		metric.WithUnit("{blob}"),
	)
	errs = append(errs, err)
	recorder.casBlobBytes, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.cas.blob.bytes",
		metric.WithDescription("Verified CAS blob bytes."),
		metric.WithUnit("By"),
	)
	errs = append(errs, err)
	recorder.publishJobs, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.publish.jobs",
		metric.WithDescription("Publish jobs by durable state."),
		metric.WithUnit("{job}"),
	)
	errs = append(errs, err)
	recorder.publishSteps, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.publish.steps",
		metric.WithDescription("Publish steps by step and durable state."),
		metric.WithUnit("{step}"),
	)
	errs = append(errs, err)
	recorder.publishStepOldestQueuedAges, err = recorder.meter.Float64ObservableGauge(
		"imgsrv.publish.step.oldest.queued.age",
		metric.WithDescription("Age of the oldest due queued publish step by step."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)
	recorder.publishStepOldestRunningAges, err = recorder.meter.Float64ObservableGauge(
		"imgsrv.publish.step.oldest.running.age",
		metric.WithDescription("Age of the oldest running publish step by step."),
		metric.WithUnit("s"),
	)
	errs = append(errs, err)
	recorder.publishingVersions, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.publish.versions.publishing",
		metric.WithDescription("Image versions currently in publishing state."),
		metric.WithUnit("{version}"),
	)
	errs = append(errs, err)
	recorder.incusProjectionRows, err = recorder.meter.Int64ObservableGauge(
		"imgsrv.incus.projection.rows",
		metric.WithDescription("Current Incus Simple Streams projection rows."),
		metric.WithUnit("{row}"),
	)
	errs = append(errs, err)

	return errors.Join(errs...)
}

func (recorder *Recorder) observePostgresPool(observer metric.Observer, provider PostgresObserver) {
	snapshot, err := provider.PoolMetrics()
	if err != nil {
		return
	}

	observer.ObserveInt64(recorder.postgresPoolConnections, snapshot.AcquiredConns, poolStateAttrs("acquired"))
	observer.ObserveInt64(recorder.postgresPoolConnections, snapshot.IdleConns, poolStateAttrs("idle"))
	observer.ObserveInt64(recorder.postgresPoolConnections, snapshot.ConstructingConns, poolStateAttrs("constructing"))
	observer.ObserveInt64(recorder.postgresPoolConnections, snapshot.TotalConns, poolStateAttrs("total"))
	observer.ObserveInt64(recorder.postgresPoolConnections, snapshot.MaxConns, poolStateAttrs("max"))
	observer.ObserveInt64(recorder.postgresPoolAcquires, snapshot.AcquireCount)
	observer.ObserveInt64(recorder.postgresPoolEmptyAcquires, snapshot.EmptyAcquireCount)
	observer.ObserveInt64(recorder.postgresPoolCanceledAcquires, snapshot.CanceledAcquireCount)
	observer.ObserveFloat64(recorder.postgresPoolAcquireDuration, snapshot.AcquireDuration.Seconds())
}

func (recorder *Recorder) observeStoreState(ctx context.Context, observer metric.Observer, provider PostgresObserver) {
	snapshot, err := provider.StoreMetrics(ctx)
	if err != nil {
		return
	}

	for _, count := range snapshot.UploadSessions {
		observer.ObserveInt64(recorder.uploadSessions, count.Count, stateAttrs(count.State))
	}
	for _, count := range snapshot.CASIngestJobs {
		observer.ObserveInt64(recorder.casIngestJobs, count.Count, stateAttrs(count.State))
	}
	if snapshot.HasCASIngestOldestQueuedAge {
		observer.ObserveFloat64(recorder.casIngestOldestQueuedAge, snapshot.CASIngestOldestQueuedAge.Seconds())
	}
	if snapshot.HasCASIngestOldestRunningAge {
		observer.ObserveFloat64(recorder.casIngestOldestRunningAge, snapshot.CASIngestOldestRunningAge.Seconds())
	}
	observer.ObserveInt64(recorder.casBlobs, snapshot.CASBlobs)
	observer.ObserveInt64(recorder.casBlobBytes, snapshot.CASBlobBytes)

	for _, count := range snapshot.PublishJobs {
		observer.ObserveInt64(recorder.publishJobs, count.Count, stateAttrs(count.State))
	}
	for _, count := range snapshot.PublishSteps {
		observer.ObserveInt64(
			recorder.publishSteps,
			count.Count,
			metric.WithAttributes(attribute.String("step", count.Step), attribute.String("state", count.State)),
		)
	}
	for _, age := range snapshot.PublishStepOldestQueuedAges {
		observer.ObserveFloat64(recorder.publishStepOldestQueuedAges, age.Age.Seconds(), stepAttrs(age.Step))
	}
	for _, age := range snapshot.PublishStepOldestRunningAges {
		observer.ObserveFloat64(recorder.publishStepOldestRunningAges, age.Age.Seconds(), stepAttrs(age.Step))
	}
	observer.ObserveInt64(recorder.publishingVersions, snapshot.PublishingVersions)
	observer.ObserveInt64(recorder.incusProjectionRows, snapshot.IncusProjectionRows)
}

func poolStateAttrs(state string) metric.ObserveOption {
	return metric.WithAttributes(attribute.String("state", state))
}

func stateAttrs(state string) metric.ObserveOption {
	return metric.WithAttributes(attribute.String("state", state))
}

func stepAttrs(step string) metric.ObserveOption {
	return metric.WithAttributes(attribute.String("step", step))
}

func jobAttrs(job string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("job", job))
}

func timestampSeconds(now time.Time) float64 {
	return float64(now.UnixNano()) / float64(time.Second)
}
