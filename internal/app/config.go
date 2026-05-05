package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultListen          = ":8080"
	defaultLogFormat       = "text"
	defaultVerbosity       = "info"
	defaultMetricsPath     = "/metrics"
	defaultShutdownTimeout = 10 * time.Second
	defaultUploadTTL       = 24 * time.Hour
	defaultNodeName        = "imgsrv"
	defaultRunIDBytes      = 5
	defaultRunIDLength     = 10
	defaultCASPollInterval = 5 * time.Second
	defaultCASErrorBackoff = 5 * time.Second
	defaultCASErrorMax     = time.Minute
	defaultCASBreakerLimit = 10
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

func defaultNodeNameValue() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return defaultNodeName
	}

	return strings.TrimSpace(hostname)
}

func defaultRunID() string {
	var token [defaultRunIDBytes]byte
	if _, err := rand.Read(token[:]); err == nil {
		return hex.EncodeToString(token[:])
	}

	return fallbackRunID(time.Now())
}

func fallbackRunID(now time.Time) string {
	token := fmt.Sprintf("%x%x", os.Getpid(), now.UnixNano())
	if len(token) <= defaultRunIDLength {
		return token
	}

	return token[len(token)-defaultRunIDLength:]
}

func (c Config) hasS3Config() bool {
	return c.S3Endpoint != "" ||
		c.S3Bucket != "" ||
		c.S3AccessKeyID != "" ||
		c.S3SecretAccessKey != "" ||
		c.S3SessionToken != "" ||
		c.S3Region != ""
}
