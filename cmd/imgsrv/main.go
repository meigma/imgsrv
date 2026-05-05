package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/meigma/imgsrv/internal/app"
	"github.com/meigma/imgsrv/internal/cli"
)

func main() {
	os.Exit(run())
}

// run wires SIGINT and SIGTERM cancellation into the CLI dispatch and returns
// the process exit code, reporting any error from cli.ExecuteContext to stderr.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cli.ExecuteContext(ctx, os.Args[1:], app.Run, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "imgsrv: %v\n", err)
		return 1
	}

	return 0
}
