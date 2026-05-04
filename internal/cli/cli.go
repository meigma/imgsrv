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
	Listen          string        `name:"listen"           env:"IMGSRV_LISTEN"           default:":8080"          help:"HTTP listen address."`
	LogFormat       string        `name:"log-format"       env:"IMGSRV_LOG_FORMAT"       default:"text"           help:"Log output format."                                       enum:"text,json"`
	Verbosity       string        `name:"verbosity"        env:"IMGSRV_VERBOSITY"        default:"info"           help:"Minimum log verbosity."                                   enum:"debug,info,warn,error"`
	MetricsListen   string        `name:"metrics-listen"   env:"IMGSRV_METRICS_LISTEN"   default:"127.0.0.1:9464" help:"Metrics HTTP listen address. Empty disables metrics."`
	MetricsPath     string        `name:"metrics-path"     env:"IMGSRV_METRICS_PATH"     default:"/metrics"       help:"Prometheus metrics path."`
	PostgresURL     string        `name:"postgres-url"     env:"IMGSRV_POSTGRES_URL"                              help:"PostgreSQL connection URL. Empty skips database startup."`
	ShutdownTimeout time.Duration `name:"shutdown-timeout" env:"IMGSRV_SHUTDOWN_TIMEOUT" default:"10s"            help:"Graceful shutdown timeout."`
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
		Listen:          command.Listen,
		LogFormat:       command.LogFormat,
		Verbosity:       command.Verbosity,
		MetricsListen:   command.MetricsListen,
		MetricsPath:     command.MetricsPath,
		PostgresURL:     command.PostgresURL,
		ShutdownTimeout: command.ShutdownTimeout,
	})
}

type exitError struct {
	code int
}

func (e exitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}
