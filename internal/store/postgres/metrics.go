package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meigma/imgsrv/internal/metrics"
)

// PoolMetrics returns a current snapshot of the shared Postgres connection pool.
func (store *Store) PoolMetrics() (metrics.PostgresPoolSnapshot, error) {
	if store == nil || store.pool == nil {
		return metrics.PostgresPoolSnapshot{}, errors.New("postgres store is closed")
	}

	stats := store.pool.Stat()

	return metrics.PostgresPoolSnapshot{
		AcquiredConns:        int64(stats.AcquiredConns()),
		IdleConns:            int64(stats.IdleConns()),
		ConstructingConns:    int64(stats.ConstructingConns()),
		TotalConns:           int64(stats.TotalConns()),
		MaxConns:             int64(stats.MaxConns()),
		AcquireCount:         stats.AcquireCount(),
		EmptyAcquireCount:    stats.EmptyAcquireCount(),
		CanceledAcquireCount: stats.CanceledAcquireCount(),
		AcquireDuration:      stats.AcquireDuration(),
	}, nil
}

// StoreMetrics returns a current snapshot of durable imgsrv service state.
func (store *Store) StoreMetrics(ctx context.Context) (metrics.StoreSnapshot, error) {
	if store == nil || store.pool == nil {
		return metrics.StoreSnapshot{}, errors.New("postgres store is closed")
	}

	snapshot := metrics.StoreSnapshot{}
	var err error
	if snapshot.UploadSessions, err = collectStateCounts(ctx, store.pool, "upload_sessions"); err != nil {
		return metrics.StoreSnapshot{}, err
	}
	if snapshot.CASIngestJobs, err = collectStateCounts(ctx, store.pool, "cas_ingest_jobs"); err != nil {
		return metrics.StoreSnapshot{}, err
	}
	snapshot.CASIngestOldestQueuedAge, snapshot.HasCASIngestOldestQueuedAge, err = collectAge(
		ctx,
		store.pool,
		`SELECT EXTRACT(EPOCH FROM now() - MIN(run_after))::double precision
		FROM cas_ingest_jobs
		WHERE state = 'queued'
			AND run_after <= now()`,
	)
	if err != nil {
		return metrics.StoreSnapshot{}, err
	}
	snapshot.CASIngestOldestRunningAge, snapshot.HasCASIngestOldestRunningAge, err = collectAge(
		ctx,
		store.pool,
		`SELECT EXTRACT(EPOCH FROM now() - MIN(locked_at))::double precision
		FROM cas_ingest_jobs
		WHERE state = 'running'`,
	)
	if err != nil {
		return metrics.StoreSnapshot{}, err
	}
	if scanErr := store.pool.QueryRow(
		ctx,
		`SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM cas_blobs`,
	).Scan(&snapshot.CASBlobs, &snapshot.CASBlobBytes); scanErr != nil {
		return metrics.StoreSnapshot{}, fmt.Errorf("collect cas blob metrics: %w", scanErr)
	}
	if snapshot.PublishJobs, err = collectStateCounts(ctx, store.pool, "publish_jobs"); err != nil {
		return metrics.StoreSnapshot{}, err
	}
	if snapshot.PublishSteps, err = collectPublishStepCounts(ctx, store.pool); err != nil {
		return metrics.StoreSnapshot{}, err
	}
	if snapshot.PublishStepOldestQueuedAges, err = collectStepAges(
		ctx,
		store.pool,
		`SELECT name, EXTRACT(EPOCH FROM now() - MIN(run_after))::double precision
		FROM publish_job_steps
		WHERE state = 'queued'
			AND run_after <= now()
		GROUP BY name
		ORDER BY name`,
	); err != nil {
		return metrics.StoreSnapshot{}, err
	}
	if snapshot.PublishStepOldestRunningAges, err = collectStepAges(
		ctx,
		store.pool,
		`SELECT name, EXTRACT(EPOCH FROM now() - MIN(locked_at))::double precision
		FROM publish_job_steps
		WHERE state = 'running'
		GROUP BY name
		ORDER BY name`,
	); err != nil {
		return metrics.StoreSnapshot{}, err
	}
	if scanErr := store.pool.QueryRow(
		ctx,
		`SELECT COUNT(*)
		FROM image_versions
		WHERE state = 'publishing'`,
	).Scan(&snapshot.PublishingVersions); scanErr != nil {
		return metrics.StoreSnapshot{}, fmt.Errorf("collect publishing version metrics: %w", scanErr)
	}
	if scanErr := store.pool.QueryRow(
		ctx,
		`SELECT COUNT(*)
		FROM incus_projection_items`,
	).Scan(&snapshot.IncusProjectionRows); scanErr != nil {
		return metrics.StoreSnapshot{}, fmt.Errorf("collect incus projection metrics: %w", scanErr)
	}

	return snapshot, nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func collectStateCounts(ctx context.Context, db queryer, table string) ([]metrics.StateCount, error) {
	rows, err := db.Query(ctx, fmt.Sprintf(
		`SELECT state, COUNT(*)
		FROM %s
		GROUP BY state
		ORDER BY state`,
		table,
	))
	if err != nil {
		return nil, fmt.Errorf("collect %s state metrics: %w", table, err)
	}
	defer rows.Close()

	var counts []metrics.StateCount
	for rows.Next() {
		var count metrics.StateCount
		if err := rows.Scan(&count.State, &count.Count); err != nil {
			return nil, fmt.Errorf("scan %s state metrics: %w", table, err)
		}
		counts = append(counts, count)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collect %s state metrics: %w", table, err)
	}

	return counts, nil
}

func collectPublishStepCounts(ctx context.Context, db queryer) ([]metrics.StepStateCount, error) {
	rows, err := db.Query(
		ctx,
		`SELECT name, state, COUNT(*)
		FROM publish_job_steps
		GROUP BY name, state
		ORDER BY name, state`,
	)
	if err != nil {
		return nil, fmt.Errorf("collect publish step metrics: %w", err)
	}
	defer rows.Close()

	var counts []metrics.StepStateCount
	for rows.Next() {
		var count metrics.StepStateCount
		if err := rows.Scan(&count.Step, &count.State, &count.Count); err != nil {
			return nil, fmt.Errorf("scan publish step metrics: %w", err)
		}
		counts = append(counts, count)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collect publish step metrics: %w", err)
	}

	return counts, nil
}

func collectAge(ctx context.Context, db queryer, query string) (time.Duration, bool, error) {
	var age sql.NullFloat64
	if err := db.QueryRow(ctx, query).Scan(&age); err != nil {
		return 0, false, fmt.Errorf("collect age metric: %w", err)
	}
	if !age.Valid {
		return 0, false, nil
	}

	return secondsDuration(age.Float64), true, nil
}

func collectStepAges(ctx context.Context, db queryer, query string) ([]metrics.StepAge, error) {
	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("collect publish step age metrics: %w", err)
	}
	defer rows.Close()

	var ages []metrics.StepAge
	for rows.Next() {
		var step string
		var age sql.NullFloat64
		if err := rows.Scan(&step, &age); err != nil {
			return nil, fmt.Errorf("scan publish step age metrics: %w", err)
		}
		if age.Valid {
			ages = append(ages, metrics.StepAge{
				Step: step,
				Age:  secondsDuration(age.Float64),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collect publish step age metrics: %w", err)
	}

	return ages, nil
}

func secondsDuration(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}

	return time.Duration(seconds * float64(time.Second))
}
