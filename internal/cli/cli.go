package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/alecthomas/kong"

	"github.com/meigma/imgsrv/internal/app"
)

// Runner starts imgsrv from resolved process configuration.
type Runner func(context.Context, app.Config) error

// rootCommand is the Kong-tagged structure that defines the imgsrv flag and
// environment variable surface. Each field maps to one resolved app.Config
// value passed to the Runner.
type rootCommand struct {
	// Listen is the HTTP listen address for the public API server.
	Listen string `name:"listen" env:"IMGSRV_LISTEN" default:":8080" help:"HTTP listen address."`
	// NodeName is the human-readable node identifier used as a prefix for
	// background worker IDs.
	NodeName string `name:"node-name" env:"IMGSRV_NODE_NAME" help:"Node name used as the prefix for background worker IDs."`
	// LogFormat selects the structured log encoding (text or json).
	LogFormat string `name:"log-format" env:"IMGSRV_LOG_FORMAT" default:"text" help:"Log output format." enum:"text,json"`
	// Verbosity selects the minimum emitted log level.
	Verbosity string `name:"verbosity" env:"IMGSRV_VERBOSITY" default:"info" help:"Minimum log verbosity." enum:"debug,info,warn,error"`
	// MetricsListen is the HTTP listen address for the Prometheus metrics
	// endpoint. An empty value disables the metrics server.
	MetricsListen string `name:"metrics-listen" env:"IMGSRV_METRICS_LISTEN" default:"127.0.0.1:9464" help:"Metrics HTTP listen address. Empty disables metrics."`
	// MetricsPath is the HTTP path that serves Prometheus metrics.
	MetricsPath string `name:"metrics-path" env:"IMGSRV_METRICS_PATH" default:"/metrics" help:"Prometheus metrics path."`
	// PostgresURL is the PostgreSQL connection URL used for the control plane.
	// An empty value skips database startup.
	PostgresURL string `name:"postgres-url" env:"IMGSRV_POSTGRES_URL" help:"PostgreSQL connection URL. Empty skips database startup."`
	// OIDCIssuerURL is the OIDC issuer used for JWT bearer authentication.
	OIDCIssuerURL string `name:"oidc-issuer-url" env:"IMGSRV_OIDC_ISSUER_URL" help:"OIDC issuer URL for JWT bearer authentication."`
	// OIDCAudience is the required audience for OIDC JWT bearer tokens.
	OIDCAudience string `name:"oidc-audience" env:"IMGSRV_OIDC_AUDIENCE" help:"Required OIDC JWT audience."`
	// OIDCRequiredScope is the token scope required before OIDC principals may write content.
	OIDCRequiredScope string `name:"oidc-required-scope" env:"IMGSRV_OIDC_REQUIRED_SCOPE" help:"Required OIDC scope for content writes."`
	// GitHubOIDCIssuerURL is the GitHub Actions OIDC issuer.
	GitHubOIDCIssuerURL string `name:"github-oidc-issuer-url" env:"IMGSRV_GITHUB_OIDC_ISSUER_URL" help:"GitHub Actions OIDC issuer URL. Empty uses GitHub's public issuer."`
	// GitHubOIDCAudience is the required audience for GitHub Actions OIDC tokens.
	GitHubOIDCAudience string `name:"github-oidc-audience" env:"IMGSRV_GITHUB_OIDC_AUDIENCE" help:"Required GitHub Actions OIDC audience."`
	// GitHubOIDCRepositoryID is the trusted GitHub repository_id claim.
	GitHubOIDCRepositoryID string `name:"github-oidc-repository-id" env:"IMGSRV_GITHUB_OIDC_REPOSITORY_ID" help:"Trusted GitHub Actions OIDC repository_id claim."`
	// GitHubOIDCWorkflowRef is the trusted GitHub workflow_ref claim.
	GitHubOIDCWorkflowRef string `name:"github-oidc-workflow-ref" env:"IMGSRV_GITHUB_OIDC_WORKFLOW_REF" help:"Trusted GitHub Actions OIDC workflow_ref claim."`
	// GitHubOIDCSubject is the trusted GitHub OIDC sub claim.
	GitHubOIDCSubject string `name:"github-oidc-subject" env:"IMGSRV_GITHUB_OIDC_SUBJECT" help:"Trusted GitHub Actions OIDC sub claim."`
	// S3Endpoint is the S3-compatible API endpoint without a URL scheme.
	S3Endpoint string `name:"s3-endpoint" env:"IMGSRV_S3_ENDPOINT" help:"S3-compatible endpoint without a URL scheme."`
	// S3Bucket is the bucket used for imgsrv object storage.
	S3Bucket string `name:"s3-bucket" env:"IMGSRV_S3_BUCKET" help:"S3 bucket for imgsrv object storage."`
	// S3AccessKeyID is the S3 access key ID.
	S3AccessKeyID string `name:"s3-access-key-id" env:"IMGSRV_S3_ACCESS_KEY_ID" help:"S3 access key ID."`
	// S3SecretAccessKey is the S3 secret access key.
	S3SecretAccessKey string `name:"s3-secret-access-key" env:"IMGSRV_S3_SECRET_ACCESS_KEY" help:"S3 secret access key."`
	// S3SessionToken is the optional temporary credential session token.
	S3SessionToken string `name:"s3-session-token" env:"IMGSRV_S3_SESSION_TOKEN" help:"Optional S3 session token."`
	// S3Region is the optional S3 region.
	S3Region string `name:"s3-region" env:"IMGSRV_S3_REGION" help:"Optional S3 region."`
	// S3UseTLS enables HTTPS when talking to the S3-compatible endpoint.
	S3UseTLS bool `name:"s3-use-tls" env:"IMGSRV_S3_USE_TLS" default:"false" help:"Use HTTPS for S3-compatible object storage."`
	// S3PathStyle forces path-style S3 bucket addressing instead of
	// virtual-hosted-style.
	S3PathStyle bool `name:"s3-path-style" env:"IMGSRV_S3_PATH_STYLE" default:"false" help:"Use path-style S3 bucket addressing."`
	// UploadTTL controls how long new upload sessions remain mutable.
	UploadTTL time.Duration `name:"upload-ttl" env:"IMGSRV_UPLOAD_TTL" default:"24h" help:"Mutable lifetime for new upload sessions."`
	// CASPromotionEnabled starts the in-process CAS promotion worker.
	CASPromotionEnabled bool `name:"cas-promotion-enabled" env:"IMGSRV_CAS_PROMOTION_ENABLED" default:"false" help:"Run the in-process CAS promotion worker."`
	// CASPromotionInterval is the idle poll interval for the CAS promotion
	// worker.
	CASPromotionInterval time.Duration `name:"cas-promotion-poll-interval" env:"IMGSRV_CAS_PROMOTION_POLL_INTERVAL" default:"5s" help:"CAS promotion worker idle poll interval."`
	// CASPromotionBackoff is the initial delay applied after a CAS promotion
	// failure.
	CASPromotionBackoff time.Duration `name:"cas-promotion-error-backoff" env:"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF" default:"5s" help:"Initial CAS promotion error backoff."`
	// CASPromotionMaxBackoff caps the delay applied after repeated CAS
	// promotion failures.
	CASPromotionMaxBackoff time.Duration `name:"cas-promotion-error-backoff-max" env:"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF_MAX" default:"1m" help:"Maximum CAS promotion error backoff."`
	// CASPromotionBreakerFailures is the number of consecutive CAS promotion
	// failures that opens the circuit breaker.
	CASPromotionBreakerFailures int `name:"cas-promotion-circuit-breaker-failures" env:"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_FAILURES" default:"10" help:"Consecutive CAS promotion failures before circuit-breaker cooldown."`
	// CASPromotionBreakerCooldown is the cooldown applied while the CAS
	// promotion circuit breaker is open.
	CASPromotionBreakerCooldown time.Duration `name:"cas-promotion-circuit-breaker-cooldown" env:"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_COOLDOWN" default:"1m" help:"CAS promotion circuit-breaker cooldown."`
	// ShutdownTimeout bounds graceful HTTP server shutdown.
	ShutdownTimeout time.Duration `name:"shutdown-timeout" env:"IMGSRV_SHUTDOWN_TIMEOUT" default:"10s" help:"Graceful shutdown timeout."`
}

// ExecuteContext parses command-line configuration and starts imgsrv.
func ExecuteContext(ctx context.Context, args []string, run Runner, stdout io.Writer, stderr io.Writer) error {
	if run == nil {
		run = func(context.Context, app.Config) error { return nil }
	}

	var command rootCommand
	exitCode := 0
	exited := false
	parser, err := kong.New(&command,
		kong.Name("imgsrv"),
		kong.Description("Image artifact service."),
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) {
			exitCode = code
			exited = true
		}),
	)
	if err != nil {
		return err
	}

	if _, err := parser.Parse(args); err != nil {
		return err
	}
	if exited {
		if exitCode == 0 {
			return nil
		}
		return exitError{code: exitCode}
	}

	return run(ctx, app.Config{
		Listen:                             command.Listen,
		NodeName:                           command.NodeName,
		LogFormat:                          command.LogFormat,
		Verbosity:                          command.Verbosity,
		MetricsListen:                      command.MetricsListen,
		MetricsPath:                        command.MetricsPath,
		PostgresURL:                        command.PostgresURL,
		OIDCIssuerURL:                      command.OIDCIssuerURL,
		OIDCAudience:                       command.OIDCAudience,
		OIDCRequiredScope:                  command.OIDCRequiredScope,
		GitHubOIDCIssuerURL:                command.GitHubOIDCIssuerURL,
		GitHubOIDCAudience:                 command.GitHubOIDCAudience,
		GitHubOIDCRepositoryID:             command.GitHubOIDCRepositoryID,
		GitHubOIDCWorkflowRef:              command.GitHubOIDCWorkflowRef,
		GitHubOIDCSubject:                  command.GitHubOIDCSubject,
		S3Endpoint:                         command.S3Endpoint,
		S3Bucket:                           command.S3Bucket,
		S3AccessKeyID:                      command.S3AccessKeyID,
		S3SecretAccessKey:                  command.S3SecretAccessKey,
		S3SessionToken:                     command.S3SessionToken,
		S3Region:                           command.S3Region,
		S3UseTLS:                           command.S3UseTLS,
		S3PathStyle:                        command.S3PathStyle,
		UploadTTL:                          command.UploadTTL,
		CASPromotionEnabled:                command.CASPromotionEnabled,
		CASPromotionPollInterval:           command.CASPromotionInterval,
		CASPromotionErrorBackoffInitial:    command.CASPromotionBackoff,
		CASPromotionErrorBackoffMax:        command.CASPromotionMaxBackoff,
		CASPromotionCircuitBreakerFailures: command.CASPromotionBreakerFailures,
		CASPromotionCircuitBreakerCooldown: command.CASPromotionBreakerCooldown,
		ShutdownTimeout:                    command.ShutdownTimeout,
	})
}

// exitError reports a non-zero exit code requested by Kong (for example, after
// a parse failure that printed help). It propagates the code to the caller so
// the binary can exit with a matching status.
type exitError struct {
	code int
}

// Error formats the captured exit code as a human-readable error message.
func (e exitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}
