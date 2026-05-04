package app

import "time"

const (
	defaultListen          = ":8080"
	defaultLogFormat       = "text"
	defaultLogLevel        = "info"
	defaultShutdownTimeout = 10 * time.Second
)

// Config contains process-level runtime configuration for imgsrv.
type Config struct {
	// Listen is the TCP address the HTTP server listens on.
	Listen string

	// LogFormat selects the process log encoding.
	LogFormat string

	// LogLevel selects the minimum emitted log level.
	LogLevel string

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
	if c.LogLevel == "" {
		c.LogLevel = defaultLogLevel
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	return c
}
