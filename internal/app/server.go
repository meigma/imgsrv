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
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/objectstore/s3"
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

	// Uploads coordinates client-facing upload writes.
	Uploads httpapi.UploadService

	// Catalog coordinates client-facing image catalog operations.
	Catalog httpapi.CatalogService

	// Blobs coordinates client-facing raw CAS blob reads.
	Blobs httpapi.BlobService

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

	var store *postgres.Store
	if cfg.PostgresURL != "" {
		store, err = postgres.Open(ctx, postgres.Config{URL: cfg.PostgresURL})
		if err != nil {
			return err
		}
	}

	uploadDependency, err := newUploadService(cfg, store)
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

	authDependency, err := newAuthService(ctx, cfg, store)
	if err != nil {
		if store != nil {
			return joinError(err, store.Close())
		}
		return err
	}

	server, err := NewServer(cfg, Dependencies{
		Logger:         logger,
		Auth:           authDependency.service,
		Uploads:        uploadDependency.service,
		Catalog:        newCatalogService(store),
		Blobs:          uploadDependency.blobs,
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
		Auth:      deps.Auth,
		Uploads:   deps.Uploads,
		Catalog:   deps.Catalog,
		Blobs:     deps.Blobs,
		UploadTTL: cfg.UploadTTL,
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
	service *httpauth.Middleware
}

// newAuthService builds authkit middleware from the shared Postgres store and configured issuers.
func newAuthService(ctx context.Context, cfg Config, store *postgres.Store) (authDependency, error) {
	if err := cfg.validateOIDCConfig(); err != nil {
		return authDependency{}, err
	}
	if err := cfg.validateGitHubOIDCConfig(); err != nil {
		return authDependency{}, err
	}
	if store == nil {
		if cfg.hasAuthConfig() {
			return authDependency{}, errors.New("postgres url is required when auth is configured")
		}
		return authDependency{}, nil
	}

	service, err := authz.NewMiddleware(ctx, authz.Config{
		Store: store.Authkit(),
		OIDC: authz.OIDCConfig{
			IssuerURL:     cfg.OIDCIssuerURL,
			Audience:      cfg.OIDCAudience,
			RequiredScope: cfg.OIDCRequiredScope,
		},
		GitHubOIDC: authz.GitHubOIDCConfig{
			IssuerURL:    cfg.GitHubOIDCIssuerURL,
			Audience:     cfg.GitHubOIDCAudience,
			RepositoryID: cfg.GitHubOIDCRepositoryID,
			WorkflowRef:  cfg.GitHubOIDCWorkflowRef,
			Subject:      cfg.GitHubOIDCSubject,
		},
		ErrorRenderer: httpapi.WriteAuthError,
	})
	if err != nil {
		return authDependency{}, err
	}

	return authDependency{
		service: service,
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

// uploadServiceDependency bundles the upload service and the underlying object store it writes to.
type uploadServiceDependency struct {
	service httpapi.UploadService
	blobs   httpapi.BlobService
	objects objectstore.Store
}

// newUploadService builds the upload service and its S3 object store from cfg, returning a zero
// value when no S3 configuration is present.
func newUploadService(cfg Config, store *postgres.Store) (uploadServiceDependency, error) {
	if !cfg.hasS3Config() {
		return uploadServiceDependency{}, nil
	}
	if store == nil {
		return uploadServiceDependency{}, errors.New("postgres url is required when s3 upload storage is configured")
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
	})
	if err != nil {
		return uploadServiceDependency{}, fmt.Errorf("open s3 object store: %w", err)
	}

	return uploadServiceDependency{
		service: uploads.NewService(uploads.ServiceConfig{
			Store:        store.Uploads(),
			Objects:      objects,
			TrustedBlobs: trustedBlobLookup(store),
		}),
		blobs:   newCASService(store, objects),
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
func newCASService(store *postgres.Store, objects objectstore.Store) *cas.Service {
	if store == nil || objects == nil {
		return nil
	}

	return cas.NewService(cas.ServiceConfig{
		Store:   store.CAS(),
		Objects: objects,
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
			errCh <- componentResult{name: spec.name + " http server", err: err}
		}(spec)
	}
	for _, spec := range s.backgroundJobs {
		go func(spec backgroundJobSpec) {
			errCh <- componentResult{name: spec.name, err: spec.job.Run(runCtx)}
		}(spec)
	}

	select {
	case <-ctx.Done():
		cancel()
		return s.shutdownComponents(servers, errCh, componentCount, nil)
	case result := <-errCh:
		cancel()
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
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	casService := newCASService(store, objects)

	return []BackgroundJob{jobs.New(jobs.Config{
		Handler: promote.New(promote.Config{
			Uploads: store.Uploads(),
			CAS:     casService,
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
