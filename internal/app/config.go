package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

	// OIDCIssuerURL is the configured issuer for generic OIDC JWT bearer tokens.
	OIDCIssuerURL string

	// OIDCAudience is the required audience for generic OIDC JWT bearer tokens.
	OIDCAudience string

	// OIDCRequiredScope is the token scope required before OIDC principals may write content.
	OIDCRequiredScope string

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

// hasOIDCConfig reports whether any generic OIDC configuration field is populated.
func (c Config) hasOIDCConfig() bool {
	return strings.TrimSpace(c.OIDCIssuerURL) != "" ||
		strings.TrimSpace(c.OIDCAudience) != "" ||
		strings.TrimSpace(c.OIDCRequiredScope) != ""
}

// validateOIDCConfig enforces all-or-nothing generic OIDC configuration.
func (c Config) validateOIDCConfig() error {
	issuerURL := strings.TrimSpace(c.OIDCIssuerURL)
	audience := strings.TrimSpace(c.OIDCAudience)
	scope := strings.TrimSpace(c.OIDCRequiredScope)
	if issuerURL == "" && audience == "" && scope == "" {
		return nil
	}
	if issuerURL == "" || audience == "" || scope == "" {
		return errors.New("oidc issuer url, audience, and required scope must be set together")
	}
	issuer, err := url.Parse(issuerURL)
	if err != nil || issuer.Scheme == "" || issuer.Host == "" {
		return errors.New("oidc issuer url must be an absolute URL")
	}
	if issuer.Scheme != "https" {
		return errors.New("oidc issuer url must use https")
	}

	return nil
}
