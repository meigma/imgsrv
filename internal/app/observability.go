package app

import (
	"context"
	"fmt"

	appmetrics "github.com/meigma/imgsrv/internal/metrics"
	"github.com/meigma/imgsrv/internal/telemetry"
)

const applicationMeterName = "github.com/meigma/imgsrv/internal/metrics"

type observability struct {
	telemetry *telemetry.Telemetry
	metrics   *appmetrics.Recorder
}

func newObservability(cfg Config) (observability, error) {
	cfg = cfg.withDefaults()
	if cfg.MetricsListen == "" {
		return observability{metrics: appmetrics.Noop()}, nil
	}

	providers, err := telemetry.New(telemetry.Config{
		ServiceName: "imgsrv",
		MetricsPath: cfg.MetricsPath,
	})
	if err != nil {
		return observability{}, err
	}

	recorder, err := appmetrics.New(providers.Meter(applicationMeterName))
	if err != nil {
		return observability{}, closeTelemetryAfterError(providers, err)
	}

	return observability{
		telemetry: providers,
		metrics:   recorder,
	}, nil
}

func recorderFromTelemetry(providers *telemetry.Telemetry) (*appmetrics.Recorder, error) {
	if providers == nil {
		return appmetrics.Noop(), nil
	}

	recorder, err := appmetrics.New(providers.Meter(applicationMeterName))
	if err != nil {
		return nil, err
	}

	return recorder, nil
}

func closeTelemetryAfterError(providers *telemetry.Telemetry, cause error) error {
	if providers == nil {
		return cause
	}
	if shutdownErr := providers.Shutdown(context.Background()); shutdownErr != nil {
		return fmt.Errorf("%w: %w", cause, shutdownErr)
	}

	return cause
}
