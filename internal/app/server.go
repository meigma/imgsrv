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

	"github.com/meigma/authkit/httpauth"

	"github.com/meigma/imgsrv/internal/authz"
	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/httpapi"
	"github.com/meigma/imgsrv/internal/jobs"
	"github.com/meigma/imgsrv/internal/jobs/promote"
	"github.com/meigma/imgsrv/internal/jobs/publishflow"
	safelog "github.com/meigma/imgsrv/internal/logging"
	incusmaterialization "github.com/meigma/imgsrv/internal/materialization/incus"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/objectstore/s3"
	"github.com/meigma/imgsrv/internal/publish"
	"github.com/meigma/imgsrv/internal/store/postgres"
	"github.com/meigma/imgsrv/internal/telemetry"
	"github.com/meigma/imgsrv/internal/uploads"
)

// defaultReadHeaderTimeout bounds how long the HTTP servers wait for request headers.
const defaultReadHeaderTimeout = 5 * time.Second

// Dependencies contains adapters required to compose the imgsrv process.
type Dependencies struct {
	// Logger receives process and adapter logs. Nil selects a discarded logger.
	Logger *slog.Logger

	// Readiness reports whether the service can accept operational traffic.
	Readiness httpapi.ReadinessChecker

	// Auth coordinates bearer authentication for write operations.
	Auth *httpauth.Middleware

	// AuthManagement coordinates operator auth-management routes.
	AuthManagement httpapi.AuthManagementService

	// Uploads coordinates client-facing upload writes.
	Uploads httpapi.UploadService

	// Catalog coordinates client-facing image catalog operations.
	Catalog httpapi.CatalogService

	// Publish coordinates durable publish workflows.
	Publish httpapi.PublishService

	// Blobs coordinates client-facing raw CAS blob reads.
	Blobs httpapi.BlobService

	// SimpleStreams coordinates Incus Simple Streams metadata reads.
	SimpleStreams httpapi.SimpleStreamsService

	// BackgroundJobs run process-local background work until shutdown.
	BackgroundJobs []BackgroundJob
}

// BackgroundJob runs process-local background work until ctx is canceled.
type BackgroundJob interface {
	// Run executes background work until ctx is canceled or the job exits.
	Run(context.Context) error
}

// Server owns the HTTP server runtime.
type Server struct {
	// apiServer is the HTTP server that handles client API traffic.
	apiServer *http.Server
	// backgroundJobs are the in-process background jobs run alongside the HTTP servers.
	backgroundJobs []backgroundJobSpec
	// metricsServer is the optional HTTP server that exposes Prometheus metrics.
	metricsServer *http.Server
	// telemetry holds the optional metrics provider wired into the API handler.
	telemetry *telemetry.Telemetry
	// logger receives Server lifecycle and component logs.
	logger *slog.Logger
	// store is the optional Postgres store closed during shutdown.
	store *postgres.Store
	// shutdownTimeout bounds graceful shutdown of HTTP servers and dependencies.
	shutdownTimeout time.Duration
}

// Run starts the imgsrv service with production process dependencies.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()

	logger, err := NewLogger(os.Stderr, cfg.LogFormat, cfg.Verbosity)
	if err != nil {
		return err
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "imgsrv starting", cfg.logAttrs()...)

	var store *postgres.Store
	if cfg.PostgresURL != "" {
		store, err = postgres.Open(ctx, postgres.Config{
			URL:    cfg.PostgresURL,
			Logger: logger.With("component", "postgres"),
		})
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "postgres store ready")
	}

	uploadDependency, err := newUploadService(cfg, store, logger)
	if err != nil {
		if store != nil {
			return joinError(err, store.Close())
		}
		return err
	}
	backgroundJobs, err := newCASPromotionJobs(cfg, store, uploadDependency.objects, logger)
	if err != nil {
		if store != nil {
			return joinError(err, store.Close())
		}
		return err
	}

	authDependency, err := newAuthService(ctx, store, os.Stdout, logger)
	if err != nil {
		if store != nil {
			return joinError(err, store.Close())
		}
		return err
	}

	catalogService := newCatalogService(store)
	publishService := newPublishService(store)
	publishJobs, err := newPublishJobs(cfg, store, catalogService, uploadDependency.blobs, logger)
	if err != nil {
		if store != nil {
			return joinError(err, store.Close())
		}
		return err
	}
	backgroundJobs = append(backgroundJobs, publishJobs...)
	server, err := NewServer(cfg, Dependencies{
		Logger:         logger,
		Auth:           authDependency.service,
		AuthManagement: authDependency.management,
		Uploads:        uploadDependency.service,
		Catalog:        catalogService,
		Publish:        publishService,
		Blobs:          uploadDependency.blobs,
		SimpleStreams:  newSimpleStreamsService(catalogService, store, logger),
		BackgroundJobs: backgroundJobs,
	})
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
		logger = safelog.Nop()
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
		Logger:         logger.With("component", "httpapi"),
		Telemetry:      telemetryProviders,
		Readiness:      deps.Readiness,
		Auth:           deps.Auth,
		AuthManagement: deps.AuthManagement,
		Uploads:        deps.Uploads,
		Catalog:        deps.Catalog,
		Publish:        deps.Publish,
		Blobs:          deps.Blobs,
		SimpleStreams:  deps.SimpleStreams,
		UploadTTL:      cfg.UploadTTL,
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
	server.backgroundJobs = backgroundJobSpecs(deps.BackgroundJobs)
	if telemetryProviders != nil {
		server.metricsServer = &http.Server{
			Addr:              cfg.MetricsListen,
			Handler:           telemetryProviders.MetricsHandler,
			ReadHeaderTimeout: defaultReadHeaderTimeout,
		}
	}

	return server, nil
}

type authDependency struct {
	service    *httpauth.Middleware
	management httpapi.AuthManagementService
}

// newAuthService builds authkit middleware from the shared Postgres store.
func newAuthService(
	ctx context.Context,
	store *postgres.Store,
	bootstrapWriter io.Writer,
	logger *slog.Logger,
) (authDependency, error) {
	if store == nil {
		return authDependency{}, nil
	}
	if logger == nil {
		logger = safelog.Nop()
	}
	logger = logger.With("component", "authz")

	authStore := store.Authkit()
	if err := authz.EnsureBuiltinRoles(ctx, authStore); err != nil {
		return authDependency{}, err
	}
	if err := authz.EnsureBootstrapAdmin(ctx, authz.BootstrapConfig{
		Store:  authStore,
		Output: bootstrapWriter,
		Logger: logger,
	}); err != nil {
		return authDependency{}, err
	}
	service, err := authz.NewMiddleware(ctx, authz.Config{
		Store:         authStore,
		Logger:        logger,
		ErrorRenderer: httpapi.NewAuthErrorRenderer(logger.With("component", "httpapi")),
	})
	if err != nil {
		return authDependency{}, err
	}

	return authDependency{
		service: service,
		management: authz.NewManagementService(authz.ManagementConfig{
			Store:  authStore,
			Logger: logger.With("component", "auth-management"),
		}),
	}, nil
}

// newCatalogService builds the catalog service from the shared Postgres store.
func newCatalogService(store *postgres.Store) httpapi.CatalogService {
	if store == nil {
		return nil
	}

	return catalog.NewService(catalog.ServiceConfig{
		Store: store.Catalog(),
	})
}

// newPublishService builds the durable publish workflow service from the shared Postgres store.
func newPublishService(store *postgres.Store) httpapi.PublishService {
	if store == nil {
		return nil
	}

	return publish.NewService(publish.ServiceConfig{
		Store: store.Publish(),
	})
}

// newSimpleStreamsService builds the Incus Simple Streams projection service.
func newSimpleStreamsService(
	catalogService httpapi.CatalogService,
	store *postgres.Store,
	logger *slog.Logger,
) httpapi.SimpleStreamsService {
	if catalogService == nil || store == nil {
		return nil
	}
	if logger == nil {
		logger = safelog.Nop()
	}

	return incusmaterialization.NewService(incusmaterialization.Config{
		Catalog: catalogService,
		Store:   store.IncusProjection(),
		Logger:  logger.With("component", "incus-materialization"),
	})
}

// uploadServiceDependency bundles the upload service and the underlying object store it writes to.
type uploadServiceDependency struct {
	service httpapi.UploadService
	blobs   httpapi.BlobService
	objects objectstore.Store
}

// newUploadService builds the upload service and its S3 object store from cfg, returning a zero
// value when no S3 configuration is present.
func newUploadService(cfg Config, store *postgres.Store, logger *slog.Logger) (uploadServiceDependency, error) {
	if !cfg.hasS3Config() {
		return uploadServiceDependency{}, nil
	}
	if store == nil {
		return uploadServiceDependency{}, errors.New("postgres url is required when s3 upload storage is configured")
	}
	if logger == nil {
		logger = safelog.Nop()
	}

	objects, err := s3.New(s3.Config{
		Endpoint:        cfg.S3Endpoint,
		Bucket:          cfg.S3Bucket,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		SessionToken:    cfg.S3SessionToken,
		Region:          cfg.S3Region,
		UseTLS:          cfg.S3UseTLS,
		PathStyle:       cfg.S3PathStyle,
		Logger:          logger.With("component", "objectstore-s3"),
	})
	if err != nil {
		return uploadServiceDependency{}, fmt.Errorf("open s3 object store: %w", err)
	}

	return uploadServiceDependency{
		service: uploads.NewService(uploads.ServiceConfig{
			Store:        store.Uploads(),
			Objects:      objects,
			TrustedBlobs: trustedBlobLookup(store),
			Logger:       logger.With("component", "uploads"),
		}),
		blobs:   newCASService(store, objects, logger),
		objects: objects,
	}, nil
}

// trustedBlobLookup returns the configured upload trusted-blob lookup when the shared upload
// store implements it.
func trustedBlobLookup(store *postgres.Store) uploads.TrustedBlobLookup {
	if store == nil {
		return nil
	}

	lookup, ok := store.Uploads().(uploads.TrustedBlobLookup)
	if !ok {
		return nil
	}

	return lookup
}

// newCASService builds the CAS blob service when both durable state and object storage are configured.
func newCASService(store *postgres.Store, objects objectstore.Store, logger *slog.Logger) *cas.Service {
	if store == nil || objects == nil {
		return nil
	}
	if logger == nil {
		logger = safelog.Nop()
	}

	return cas.NewService(cas.ServiceConfig{
		Store:   store.CAS(),
		Objects: objects,
		Logger:  logger.With("component", "cas"),
	})
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

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	componentCount := len(servers) + len(s.backgroundJobs)
	errCh := make(chan componentResult, componentCount)
	for _, spec := range servers {
		go func(spec serverSpec) {
			s.logger.Info("http server listening", "name", spec.name, "addr", spec.listener.Addr().String())
			err := spec.server.Serve(spec.listener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			if err != nil {
				s.logger.Error("http server exited", "name", spec.name, "error", err)
			} else {
				s.logger.Info("http server stopped", "name", spec.name)
			}
			errCh <- componentResult{name: spec.name + " http server", err: err}
		}(spec)
	}
	for _, spec := range s.backgroundJobs {
		go func(spec backgroundJobSpec) {
			s.logger.Info("background job started", "name", spec.name)
			defer s.logger.Info("background job stopped", "name", spec.name)
			errCh <- componentResult{name: spec.name, err: spec.job.Run(runCtx)}
		}(spec)
	}

	select {
	case <-ctx.Done():
		cancel()
		s.logger.InfoContext(ctx, "shutdown requested", "reason", ctx.Err())
		return s.shutdownComponents(servers, errCh, componentCount, nil)
	case result := <-errCh:
		cancel()
		if result.err != nil {
			s.logger.ErrorContext(ctx, "component exited with error", "name", result.name, "error", result.err)
		} else {
			s.logger.InfoContext(ctx, "component exited", "name", result.name)
		}
		shutdownErr := s.shutdownComponents(servers, errCh, componentCount, &result)
		if result.err != nil {
			return joinError(formatComponentError(result), shutdownErr)
		}
		return shutdownErr
	}
}

// servers returns the HTTP server specs that should run, requiring a metrics listener when the
// metrics server is enabled.
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

// shutdownComponents stops the HTTP servers and dependencies, then drains pending component
// results into a joined error within the configured shutdown timeout.
func (s *Server) shutdownComponents(
	servers []serverSpec,
	errCh <-chan componentResult,
	componentCount int,
	first *componentResult,
) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	var joined error
	for _, spec := range servers {
		s.logger.Info("stopping http server", "name", spec.name)
		if err := spec.server.Shutdown(shutdownCtx); err != nil {
			joined = joinError(joined, fmt.Errorf("shutdown %s server: %w", spec.name, err))
		}
	}
	if s.telemetry != nil {
		if err := s.telemetry.Shutdown(shutdownCtx); err != nil {
			joined = joinError(joined, err)
		}
	}
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			joined = joinError(joined, fmt.Errorf("close postgres store: %w", err))
		}
	}

	pending := componentCount
	if first != nil {
		pending--
		if first.err != nil {
			joined = joinError(joined, formatComponentError(*first))
		}
	}
	for range pending {
		select {
		case result := <-errCh:
			if result.err != nil {
				joined = joinError(joined, formatComponentError(result))
			}
		case <-shutdownCtx.Done():
			return joinError(joined, shutdownCtx.Err())
		}
	}

	if joined != nil {
		s.logger.Error("shutdown completed with errors", "error", joined)
	} else {
		s.logger.Info("shutdown complete")
	}

	return joined
}

// serverSpec pairs a named HTTP server with the listener it should serve on.
type serverSpec struct {
	name     string
	server   *http.Server
	listener net.Listener
}

// backgroundJobSpec pairs a BackgroundJob with the name used in lifecycle logging and errors.
type backgroundJobSpec struct {
	name string
	job  BackgroundJob
}

// componentResult is the exit result of a server or background job goroutine.
type componentResult struct {
	name string
	err  error
}

// formatComponentError wraps a componentResult error with the component name for reporting.
func formatComponentError(result componentResult) error {
	return fmt.Errorf("%s: %w", result.name, result.err)
}

// newCASPromotionJobs constructs the in-process CAS promotion background jobs when promotion is
// enabled, returning an empty slice when it is disabled.
func newCASPromotionJobs(
	cfg Config,
	store *postgres.Store,
	objects objectstore.Store,
	logger *slog.Logger,
) ([]BackgroundJob, error) {
	cfg = cfg.withDefaults()
	if !cfg.CASPromotionEnabled {
		return []BackgroundJob{}, nil
	}
	if store == nil {
		return nil, errors.New("postgres url is required when cas promotion worker is enabled")
	}
	if objects == nil {
		return nil, errors.New("s3 upload storage is required when cas promotion worker is enabled")
	}
	if logger == nil {
		logger = safelog.Nop()
	}

	casService := newCASService(store, objects, logger)

	return []BackgroundJob{jobs.New(jobs.Config{
		Handler: promote.New(promote.Config{
			Uploads: store.Uploads(),
			CAS:     casService,
			Logger:  logger.With("component", "cas-promotion"),
		}),
		WorkerID:               jobs.Identity{NodeName: cfg.NodeName, RunID: cfg.RunID}.WorkerID("cas-promotion"),
		Interval:               cfg.CASPromotionPollInterval,
		ErrorBackoffInitial:    cfg.CASPromotionErrorBackoffInitial,
		ErrorBackoffMax:        cfg.CASPromotionErrorBackoffMax,
		CircuitBreakerFailures: cfg.CASPromotionCircuitBreakerFailures,
		CircuitBreakerCooldown: cfg.CASPromotionCircuitBreakerCooldown,
		Logger:                 logger.With("component", "cas-promotion"),
	})}, nil
}

// newPublishJobs constructs the in-process durable publish background job when Postgres is configured.
func newPublishJobs(
	cfg Config,
	store *postgres.Store,
	catalogService httpapi.CatalogService,
	blobs httpapi.BlobService,
	logger *slog.Logger,
) ([]BackgroundJob, error) {
	cfg = cfg.withDefaults()
	if store == nil {
		return []BackgroundJob{}, nil
	}
	if catalogService == nil {
		return nil, errors.New("catalog service is required when publish worker is enabled")
	}
	if blobs == nil {
		return nil, errors.New("blob service is required when publish worker is enabled")
	}
	if logger == nil {
		logger = safelog.Nop()
	}

	return []BackgroundJob{jobs.New(jobs.Config{
		Handler: publishflow.New(publishflow.Config{
			Store: store.Publish(),
			Incus: incusmaterialization.NewIndexer(incusmaterialization.IndexerConfig{
				Catalog: catalogService,
				Blobs:   blobs,
				Logger:  logger.With("component", "incus-indexer"),
			}),
			Logger: logger.With("component", "publish"),
		}),
		WorkerID:               jobs.Identity{NodeName: cfg.NodeName, RunID: cfg.RunID}.WorkerID("publish"),
		Interval:               defaultCASPollInterval,
		ErrorBackoffInitial:    defaultCASErrorBackoff,
		ErrorBackoffMax:        defaultCASErrorMax,
		CircuitBreakerFailures: defaultCASBreakerLimit,
		CircuitBreakerCooldown: defaultCASBreakerPause,
		Logger:                 logger.With("component", "publish"),
	})}, nil
}

// backgroundJobSpecs wraps each non-nil BackgroundJob in a backgroundJobSpec with a derived name.
func backgroundJobSpecs(jobs []BackgroundJob) []backgroundJobSpec {
	specs := make([]backgroundJobSpec, 0, len(jobs))
	for index, job := range jobs {
		if job == nil {
			continue
		}

		specs = append(specs, backgroundJobSpec{
			name: fmt.Sprintf("background-%d", index+1),
			job:  job,
		})
	}

	return specs
}

// joinError returns existing and next combined via [errors.Join], treating either nil as the other.
func joinError(existing error, next error) error {
	if next == nil {
		return existing
	}
	if existing == nil {
		return next
	}
	return errors.Join(existing, next)
}
