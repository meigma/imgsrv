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

type rootCommand struct {
	Listen                      string        `name:"listen"                                 env:"IMGSRV_LISTEN"                                 default:":8080"          help:"HTTP listen address."`
	NodeName                    string        `name:"node-name"                              env:"IMGSRV_NODE_NAME"                                                       help:"Node name used as the prefix for background worker IDs."`
	LogFormat                   string        `name:"log-format"                             env:"IMGSRV_LOG_FORMAT"                             default:"text"           help:"Log output format."                                                  enum:"text,json"`
	Verbosity                   string        `name:"verbosity"                              env:"IMGSRV_VERBOSITY"                              default:"info"           help:"Minimum log verbosity."                                              enum:"debug,info,warn,error"`
	MetricsListen               string        `name:"metrics-listen"                         env:"IMGSRV_METRICS_LISTEN"                         default:"127.0.0.1:9464" help:"Metrics HTTP listen address. Empty disables metrics."`
	MetricsPath                 string        `name:"metrics-path"                           env:"IMGSRV_METRICS_PATH"                           default:"/metrics"       help:"Prometheus metrics path."`
	PostgresURL                 string        `name:"postgres-url"                           env:"IMGSRV_POSTGRES_URL"                                                    help:"PostgreSQL connection URL. Empty skips database startup."`
	S3Endpoint                  string        `name:"s3-endpoint"                            env:"IMGSRV_S3_ENDPOINT"                                                     help:"S3-compatible endpoint without a URL scheme."`
	S3Bucket                    string        `name:"s3-bucket"                              env:"IMGSRV_S3_BUCKET"                                                       help:"S3 bucket for imgsrv object storage."`
	S3AccessKeyID               string        `name:"s3-access-key-id"                       env:"IMGSRV_S3_ACCESS_KEY_ID"                                                help:"S3 access key ID."`
	S3SecretAccessKey           string        `name:"s3-secret-access-key"                   env:"IMGSRV_S3_SECRET_ACCESS_KEY"                                            help:"S3 secret access key."`
	S3SessionToken              string        `name:"s3-session-token"                       env:"IMGSRV_S3_SESSION_TOKEN"                                                help:"Optional S3 session token."`
	S3Region                    string        `name:"s3-region"                              env:"IMGSRV_S3_REGION"                                                       help:"Optional S3 region."`
	S3UseTLS                    bool          `name:"s3-use-tls"                             env:"IMGSRV_S3_USE_TLS"                             default:"false"          help:"Use HTTPS for S3-compatible object storage."`
	S3PathStyle                 bool          `name:"s3-path-style"                          env:"IMGSRV_S3_PATH_STYLE"                          default:"false"          help:"Use path-style S3 bucket addressing."`
	UploadTTL                   time.Duration `name:"upload-ttl"                             env:"IMGSRV_UPLOAD_TTL"                             default:"24h"            help:"Mutable lifetime for new upload sessions."`
	CASPromotionEnabled         bool          `name:"cas-promotion-enabled"                  env:"IMGSRV_CAS_PROMOTION_ENABLED"                  default:"false"          help:"Run the in-process CAS promotion worker."`
	CASPromotionInterval        time.Duration `name:"cas-promotion-poll-interval"            env:"IMGSRV_CAS_PROMOTION_POLL_INTERVAL"            default:"5s"             help:"CAS promotion worker idle poll interval."`
	CASPromotionBackoff         time.Duration `name:"cas-promotion-error-backoff"            env:"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF"            default:"5s"             help:"Initial CAS promotion error backoff."`
	CASPromotionMaxBackoff      time.Duration `name:"cas-promotion-error-backoff-max"        env:"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF_MAX"        default:"1m"             help:"Maximum CAS promotion error backoff."`
	CASPromotionBreakerFailures int           `name:"cas-promotion-circuit-breaker-failures" env:"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_FAILURES" default:"10"             help:"Consecutive CAS promotion failures before circuit-breaker cooldown."`
	CASPromotionBreakerCooldown time.Duration `name:"cas-promotion-circuit-breaker-cooldown" env:"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_COOLDOWN" default:"1m"             help:"CAS promotion circuit-breaker cooldown."`
	ShutdownTimeout             time.Duration `name:"shutdown-timeout"                       env:"IMGSRV_SHUTDOWN_TIMEOUT"                       default:"10s"            help:"Graceful shutdown timeout."`
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

type exitError struct {
	code int
}

func (e exitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}
