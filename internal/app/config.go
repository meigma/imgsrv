package app

import "time"

const (
	defaultListen          = ":8080"
	defaultLogFormat       = "text"
	defaultVerbosity       = "info"
	defaultMetricsPath     = "/metrics"
	defaultShutdownTimeout = 10 * time.Second
	defaultUploadTTL       = 24 * time.Hour
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

	// PostgresURL is the PostgreSQL connection URL used for the control plane.
	// Empty skips database startup for operational-only runs.
	PostgresURL string

	// S3Endpoint is the S3-compatible API endpoint without a URL scheme.
	S3Endpoint string

	// S3Bucket is the bucket that stores imgsrv objects.
	S3Bucket string

	// S3AccessKeyID is the S3 access key ID.
	S3AccessKeyID string

	// S3SecretAccessKey is the S3 secret access key.
	S3SecretAccessKey string

	// S3SessionToken is the optional temporary credential session token.
	S3SessionToken string

	// S3Region is the optional S3 region.
	S3Region string

	// S3UseTLS enables HTTPS for S3-compatible object storage.
	S3UseTLS bool

	// S3PathStyle forces path-style S3 bucket addressing.
	S3PathStyle bool

	// UploadTTL controls how long new upload sessions remain mutable.
	UploadTTL time.Duration

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
	if c.UploadTTL == 0 {
		c.UploadTTL = defaultUploadTTL
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	return c
}

func (c Config) hasS3Config() bool {
	return c.S3Endpoint != "" ||
		c.S3Bucket != "" ||
		c.S3AccessKeyID != "" ||
		c.S3SecretAccessKey != "" ||
		c.S3SessionToken != "" ||
		c.S3Region != ""
}
