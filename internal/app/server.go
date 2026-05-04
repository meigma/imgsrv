package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/meigma/imgsrv/internal/httpapi"
)

const defaultReadHeaderTimeout = 5 * time.Second

// Dependencies contains adapters required to compose the imgsrv process.
type Dependencies struct {
	// Logger receives process and adapter logs. Nil selects a discarded logger.
	Logger *slog.Logger

	// Readiness reports whether the service can accept operational traffic.
	Readiness httpapi.ReadinessChecker
}

// Server owns the HTTP server runtime.
type Server struct {
	httpServer      *http.Server
	logger          *slog.Logger
	shutdownTimeout time.Duration
}

// Run starts the imgsrv service with production process dependencies.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()

	logger, err := NewLogger(os.Stderr, cfg.LogFormat, cfg.LogLevel)
	if err != nil {
		return err
	}

	server, err := NewServer(cfg, Dependencies{Logger: logger})
	if err != nil {
		return err
	}

	return server.Run(ctx)
}

// NewServer constructs an HTTP server from runtime configuration and adapters.
func NewServer(cfg Config, deps Dependencies) (*Server, error) {
	cfg = cfg.withDefaults()
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	handler := httpapi.New(httpapi.Dependencies{
		Logger:    logger,
		Readiness: deps.Readiness,
	})

	return &Server{
		httpServer: &http.Server{
			Addr:              cfg.Listen,
			Handler:           handler,
			ReadHeaderTimeout: defaultReadHeaderTimeout,
		},
		logger:          logger,
		shutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}

// Run listens on the configured address and serves HTTP until the context ends.
func (s *Server) Run(ctx context.Context) error {
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}

	return s.Serve(ctx, listener)
}

// Serve serves HTTP on listener until the context ends or the server fails.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", listener.Addr().String())
		errCh <- s.httpServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
