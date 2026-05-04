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
	Listen          string        `name:"listen"           env:"IMGSRV_LISTEN"           default:":8080" help:"HTTP listen address."`
	LogFormat       string        `name:"log-format"       env:"IMGSRV_LOG_FORMAT"       default:"text"  help:"Log output format."         enum:"text,json"`
	LogLevel        string        `name:"log-level"        env:"IMGSRV_LOG_LEVEL"        default:"info"  help:"Minimum log level."         enum:"debug,info,warn,error"`
	ShutdownTimeout time.Duration `name:"shutdown-timeout" env:"IMGSRV_SHUTDOWN_TIMEOUT" default:"10s"   help:"Graceful shutdown timeout."`
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
		LogLevel:        command.LogLevel,
		ShutdownTimeout: command.ShutdownTimeout,
	})
}

type exitError struct {
	code int
}

func (e exitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}
