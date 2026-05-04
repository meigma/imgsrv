package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/meigma/imgsrv/internal/httpapi"
	"github.com/meigma/imgsrv/internal/store/postgres"
	"github.com/meigma/imgsrv/internal/telemetry"
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
	apiServer       *http.Server
	metricsServer   *http.Server
	telemetry       *telemetry.Telemetry
	logger          *slog.Logger
	store           *postgres.Store
	shutdownTimeout time.Duration
}

// Run starts the imgsrv service with production process dependencies.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()

	logger, err := NewLogger(os.Stderr, cfg.LogFormat, cfg.Verbosity)
	if err != nil {
		return err
	}

	var store *postgres.Store
	if cfg.PostgresURL != "" {
		store, err = postgres.Open(ctx, postgres.Config{URL: cfg.PostgresURL})
		if err != nil {
			return err
		}
	}

	server, err := NewServer(cfg, Dependencies{Logger: logger})
	if err != nil {
		if store != nil {
			return joinError(err, store.Close())
		}
		return err
	}
	server.store = store

	runErr := server.Run(ctx)
	if store != nil {
		runErr = joinError(runErr, store.Close())
	}

	return runErr
}

// NewServer constructs an HTTP server from runtime configuration and adapters.
func NewServer(cfg Config, deps Dependencies) (*Server, error) {
	cfg = cfg.withDefaults()
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	var telemetryProviders *telemetry.Telemetry
	if cfg.MetricsListen != "" {
		var err error
		telemetryProviders, err = telemetry.New(telemetry.Config{
			ServiceName: "imgsrv",
			MetricsPath: cfg.MetricsPath,
		})
		if err != nil {
			return nil, err
		}
	}

	handler := httpapi.New(httpapi.Dependencies{
		Logger:    logger.With("component", "httpapi"),
		Telemetry: telemetryProviders,
		Readiness: deps.Readiness,
	})

	server := &Server{
		apiServer: &http.Server{
			Addr:              cfg.Listen,
			Handler:           handler,
			ReadHeaderTimeout: defaultReadHeaderTimeout,
		},
		telemetry:       telemetryProviders,
		logger:          logger,
		shutdownTimeout: cfg.ShutdownTimeout,
	}
	if telemetryProviders != nil {
		server.metricsServer = &http.Server{
			Addr:              cfg.MetricsListen,
			Handler:           telemetryProviders.MetricsHandler,
			ReadHeaderTimeout: defaultReadHeaderTimeout,
		}
	}

	return server, nil
}

// Run listens on the configured address and serves HTTP until the context ends.
func (s *Server) Run(ctx context.Context) error {
	apiListener, err := new(net.ListenConfig).Listen(ctx, "tcp", s.apiServer.Addr)
	if err != nil {
		return err
	}

	var metricsListener net.Listener
	if s.metricsServer != nil {
		metricsListener, err = new(net.ListenConfig).Listen(ctx, "tcp", s.metricsServer.Addr)
		if err != nil {
			_ = apiListener.Close()
			return err
		}
	}

	return s.Serve(ctx, apiListener, metricsListener)
}

// Serve serves HTTP on listener until the context ends or the server fails.
func (s *Server) Serve(ctx context.Context, apiListener net.Listener, metricsListener ...net.Listener) error {
	servers, err := s.servers(apiListener, metricsListener...)
	if err != nil {
		return err
	}

	errCh := make(chan serverResult, len(servers))
	for _, spec := range servers {
		go func(spec serverSpec) {
			s.logger.Info("http server listening", "name", spec.name, "addr", spec.listener.Addr().String())
			err := spec.server.Serve(spec.listener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errCh <- serverResult{name: spec.name, err: err}
		}(spec)
	}

	select {
	case <-ctx.Done():
		return s.shutdownServers(servers, errCh, nil)
	case result := <-errCh:
		shutdownErr := s.shutdownServers(servers, errCh, &result)
		if result.err != nil {
			return joinError(fmt.Errorf("%s http server: %w", result.name, result.err), shutdownErr)
		}
		return shutdownErr
	}
}

func (s *Server) servers(apiListener net.Listener, metricsListener ...net.Listener) ([]serverSpec, error) {
	if apiListener == nil {
		return nil, errors.New("api listener is required")
	}

	servers := []serverSpec{{
		name:     "api",
		server:   s.apiServer,
		listener: apiListener,
	}}
	if s.metricsServer == nil {
		return servers, nil
	}
	if len(metricsListener) == 0 || metricsListener[0] == nil {
		return nil, errors.New("metrics listener is required when metrics are enabled")
	}

	servers = append(servers, serverSpec{
		name:     "metrics",
		server:   s.metricsServer,
		listener: metricsListener[0],
	})
	return servers, nil
}

func (s *Server) shutdownServers(
	servers []serverSpec,
	errCh <-chan serverResult,
	first *serverResult,
) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	var joined error
	for _, spec := range servers {
		if err := spec.server.Shutdown(shutdownCtx); err != nil {
			joined = joinError(joined, fmt.Errorf("shutdown %s server: %w", spec.name, err))
		}
	}
	if err := s.telemetry.Shutdown(shutdownCtx); err != nil {
		joined = joinError(joined, err)
	}
	if err := s.store.Close(); err != nil {
		joined = joinError(joined, fmt.Errorf("close postgres store: %w", err))
	}
	if joined != nil {
		return joined
	}

	pending := len(servers)
	if first != nil {
		pending--
		if first.err != nil {
			joined = fmt.Errorf("%s http server: %w", first.name, first.err)
		}
	}
	for range pending {
		select {
		case result := <-errCh:
			if result.err != nil {
				joined = joinError(joined, fmt.Errorf("%s http server: %w", result.name, result.err))
			}
		case <-shutdownCtx.Done():
			return joinError(joined, shutdownCtx.Err())
		}
	}

	return joined
}

type serverSpec struct {
	name     string
	server   *http.Server
	listener net.Listener
}

type serverResult struct {
	name string
	err  error
}

func joinError(existing error, next error) error {
	if next == nil {
		return existing
	}
	if existing == nil {
		return next
	}
	return errors.Join(existing, next)
}
