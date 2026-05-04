package app

import "time"

const (
	defaultListen          = ":8080"
	defaultLogFormat       = "text"
	defaultVerbosity       = "info"
	defaultMetricsPath     = "/metrics"
	defaultShutdownTimeout = 10 * time.Second
)

// Config contains process-level runtime configuration for imgsrv.
type Config struct {
	// Listen is the TCP address the HTTP server listens on.
	Listen string

	// LogFormat selects the process log encoding.
	LogFormat string

	// Verbosity selects the minimum emitted log level.
	Verbosity string

	// MetricsListen is the TCP address the Prometheus metrics server listens on.
	// Empty disables the metrics server.
	MetricsListen string

	// MetricsPath is the HTTP path that serves Prometheus metrics.
	MetricsPath string

	// ShutdownTimeout bounds graceful HTTP server shutdown.
	ShutdownTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.LogFormat == "" {
		c.LogFormat = defaultLogFormat
	}
	if c.Verbosity == "" {
		c.Verbosity = defaultVerbosity
	}
	if c.MetricsPath == "" {
		c.MetricsPath = defaultMetricsPath
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	return c
}
