package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// defaultListen is the fallback TCP address for the API server.
	defaultListen = ":8080"
	// defaultLogFormat is the fallback log encoding.
	defaultLogFormat = "text"
	// defaultVerbosity is the fallback minimum log level.
	defaultVerbosity = "info"
	// defaultMetricsPath is the fallback HTTP path for Prometheus metrics.
	defaultMetricsPath = "/metrics"
	// defaultShutdownTimeout bounds graceful HTTP shutdown when not configured.
	defaultShutdownTimeout = 10 * time.Second
	// defaultUploadTTL is the fallback mutability window for new upload sessions.
	defaultUploadTTL = 24 * time.Hour
	// defaultNodeName is the fallback node name when the OS hostname is not usable.
	defaultNodeName = "imgsrv"
	// defaultRunIDBytes is the number of random bytes drawn for a generated run ID.
	defaultRunIDBytes = 5
	// defaultRunIDLength caps the encoded length of a fallback run ID.
	defaultRunIDLength = 10
	// defaultCASPollInterval is the fallback idle poll interval for the CAS promotion worker.
	defaultCASPollInterval = 5 * time.Second
	// defaultCASErrorBackoff is the fallback initial delay after a CAS promotion failure.
	defaultCASErrorBackoff = 5 * time.Second
	// defaultCASErrorMax caps the fallback delay after repeated CAS promotion failures.
	defaultCASErrorMax = time.Minute
	// defaultCASBreakerLimit is the fallback consecutive failure threshold that opens the CAS promotion circuit.
	defaultCASBreakerLimit = 10
	// defaultCASBreakerPause is the fallback delay observed while the CAS promotion circuit is open.
	defaultCASBreakerPause = time.Minute
)

// Config contains process-level runtime configuration for imgsrv.
type Config struct {
	// Listen is the TCP address the HTTP server listens on.
	Listen string

	// NodeName identifies the process node for derived background worker IDs.
	NodeName string

	// RunID identifies one process run for derived background worker IDs.
	RunID string

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

	// CASPromotionEnabled starts the in-process CAS promotion worker.
	CASPromotionEnabled bool

	// CASPromotionPollInterval controls how often the CAS promotion worker polls when idle.
	CASPromotionPollInterval time.Duration

	// CASPromotionErrorBackoffInitial controls the first delay after a CAS promotion failure.
	CASPromotionErrorBackoffInitial time.Duration

	// CASPromotionErrorBackoffMax caps the delay after repeated CAS promotion failures.
	CASPromotionErrorBackoffMax time.Duration

	// CASPromotionCircuitBreakerFailures opens the circuit after this many consecutive failures.
	CASPromotionCircuitBreakerFailures int

	// CASPromotionCircuitBreakerCooldown controls the delay after the CAS promotion circuit opens.
	CASPromotionCircuitBreakerCooldown time.Duration

	// ShutdownTimeout bounds graceful HTTP server shutdown.
	ShutdownTimeout time.Duration
}

// withDefaults returns a copy of c with unset fields replaced by their package defaults.
func (c Config) withDefaults() Config {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if strings.TrimSpace(c.NodeName) == "" {
		c.NodeName = defaultNodeNameValue()
	} else {
		c.NodeName = strings.TrimSpace(c.NodeName)
	}
	if strings.TrimSpace(c.RunID) == "" {
		c.RunID = defaultRunID()
	} else {
		c.RunID = strings.TrimSpace(c.RunID)
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
	if c.CASPromotionPollInterval == 0 {
		c.CASPromotionPollInterval = defaultCASPollInterval
	}
	if c.CASPromotionErrorBackoffInitial == 0 {
		c.CASPromotionErrorBackoffInitial = defaultCASErrorBackoff
	}
	if c.CASPromotionErrorBackoffMax == 0 {
		c.CASPromotionErrorBackoffMax = defaultCASErrorMax
	}
	if c.CASPromotionCircuitBreakerFailures == 0 {
		c.CASPromotionCircuitBreakerFailures = defaultCASBreakerLimit
	}
	if c.CASPromotionCircuitBreakerCooldown == 0 {
		c.CASPromotionCircuitBreakerCooldown = defaultCASBreakerPause
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	return c
}

// defaultNodeNameValue returns the OS hostname or defaultNodeName when the hostname is unavailable.
func defaultNodeNameValue() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return defaultNodeName
	}

	return strings.TrimSpace(hostname)
}

// defaultRunID returns a hex-encoded random run ID, falling back to fallbackRunID on RNG failure.
func defaultRunID() string {
	var token [defaultRunIDBytes]byte
	if _, err := rand.Read(token[:]); err == nil {
		return hex.EncodeToString(token[:])
	}

	return fallbackRunID(time.Now())
}

// fallbackRunID derives a bounded-length run ID from the process ID and the supplied time.
func fallbackRunID(now time.Time) string {
	token := fmt.Sprintf("%x%x", os.Getpid(), now.UnixNano())
	if len(token) <= defaultRunIDLength {
		return token
	}

	return token[len(token)-defaultRunIDLength:]
}

// hasS3Config reports whether any S3 configuration field is populated.
func (c Config) hasS3Config() bool {
	return c.S3Endpoint != "" ||
		c.S3Bucket != "" ||
		c.S3AccessKeyID != "" ||
		c.S3SecretAccessKey != "" ||
		c.S3SessionToken != "" ||
		c.S3Region != ""
}

// logAttrs returns a sanitized runtime configuration snapshot for process startup logs.
func (c Config) logAttrs() []slog.Attr {
	c = c.withDefaults()
	attrs := []slog.Attr{
		slog.String("listen", c.Listen),
		slog.String("node_name", c.NodeName),
		slog.String("run_id", c.RunID),
		slog.String("log_format", c.LogFormat),
		slog.String("verbosity", c.Verbosity),
		slog.Duration("upload_ttl", c.UploadTTL),
		slog.Bool("cas_promotion_enabled", c.CASPromotionEnabled),
		slog.Duration("cas_promotion_poll_interval", c.CASPromotionPollInterval),
		slog.Duration("cas_promotion_error_backoff_initial", c.CASPromotionErrorBackoffInitial),
		slog.Duration("cas_promotion_error_backoff_max", c.CASPromotionErrorBackoffMax),
		slog.Int("cas_promotion_circuit_breaker_failures", c.CASPromotionCircuitBreakerFailures),
		slog.Duration("cas_promotion_circuit_breaker_cooldown", c.CASPromotionCircuitBreakerCooldown),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
	}
	if c.MetricsListen != "" {
		attrs = append(attrs,
			slog.Bool("metrics_enabled", true),
			slog.String("metrics_listen", c.MetricsListen),
			slog.String("metrics_path", c.MetricsPath),
		)
	} else {
		attrs = append(attrs, slog.Bool("metrics_enabled", false))
	}
	attrs = append(attrs, slog.Group("postgres", sanitizedPostgresAttrs(c.PostgresURL)...))
	if c.hasS3Config() {
		attrs = append(attrs, slog.Group(
			"s3",
			slog.Bool("configured", true),
			slog.String("endpoint", c.S3Endpoint),
			slog.String("bucket", c.S3Bucket),
			slog.String("region", c.S3Region),
			slog.Bool("use_tls", c.S3UseTLS),
			slog.Bool("path_style", c.S3PathStyle),
		))
	} else {
		attrs = append(attrs, slog.Group("s3", slog.Bool("configured", false)))
	}

	return attrs
}

func sanitizedPostgresAttrs(rawURL string) []any {
	if strings.TrimSpace(rawURL) == "" {
		return []any{slog.Bool("configured", false)}
	}

	attrs := []any{slog.Bool("configured", true)}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return append(attrs, slog.Bool("parse_error", true))
	}
	if parsed.Host != "" {
		attrs = append(attrs, slog.String("host", parsed.Host))
	}
	if database := strings.TrimPrefix(parsed.EscapedPath(), "/"); database != "" {
		attrs = append(attrs, slog.String("database", database))
	}
	if sslMode := parsed.Query().Get("sslmode"); sslMode != "" {
		attrs = append(attrs, slog.String("sslmode", sslMode))
	}

	return attrs
}
