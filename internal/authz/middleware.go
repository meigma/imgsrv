package authz

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/meigma/authkit/httpauth"
	authkitoidc "github.com/meigma/authkit/oidc"
	"github.com/meigma/authkit/roleauth"

	safelog "github.com/meigma/imgsrv/internal/logging"
)

const (
	// DefaultGitHubOIDCIssuerURL is GitHub Actions' public OIDC issuer.
	DefaultGitHubOIDCIssuerURL = "https://token.actions.githubusercontent.com"

	factIdentityProvider authkit.FactKey = "identity.provider"
	factIdentitySubject  authkit.FactKey = "identity.subject"
	factIdentityClaims   authkit.FactKey = "identity.claims"
)

// Store is the authkit storage contract imgsrv needs at runtime.
type Store interface {
	apikey.TokenStore
	authkitoidc.ProviderSource
	authkit.PrincipalLister
	authkit.PrincipalResolver
	authkit.IdentityProvisioner
	authkit.PrincipalActionResolver
	authkit.ProvisioningRuleLister
}

// Config configures authkit HTTP middleware for imgsrv.
type Config struct {
	Store Store

	HTTPClient    *http.Client
	ErrorRenderer httpauth.ErrorRenderer
	Logger        *slog.Logger
}

// NewMiddleware builds the authkit HTTP middleware used by protected imgsrv routes.
func NewMiddleware(_ context.Context, cfg Config) (*httpauth.Middleware, error) {
	if cfg.Store == nil {
		return nil, errors.New("authz: authkit store is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = safelog.Nop()
	}

	authenticators, err := authenticators(cfg)
	if err != nil {
		return nil, err
	}
	roleAuthorizer, err := roleauth.NewAuthorizer(cfg.Store)
	if err != nil {
		return nil, err
	}
	policy := Authorizer{
		roles: roleAuthorizer,
	}
	resolver := policyResolver{
		store:  cfg.Store,
		logger: logger.With("component", "authz-resolver"),
	}
	pipeline, err := authkit.NewPipeline(authkit.PipelineOptions{
		Authenticators: authenticators,
		Resolver:       &resolver,
		Authorizer:     &policy,
	})
	if err != nil {
		return nil, err
	}

	var opts []httpauth.Option
	if cfg.ErrorRenderer != nil {
		opts = append(opts, httpauth.WithErrorRenderer(cfg.ErrorRenderer))
	}
	return httpauth.NewMiddleware(pipeline, opts...)
}

// FactsForAuthentication returns authorization facts extracted from a verified authkit identity.
func FactsForAuthentication(authentication authkit.Authentication) authkit.Facts {
	return authkit.Facts{
		factIdentityProvider: authentication.Identity.Provider,
		factIdentitySubject:  authentication.Identity.Subject,
		factIdentityClaims:   authentication.Identity.Claims,
	}
}

func authenticators(cfg Config) ([]authkit.Authenticator, error) {
	var result []authkit.Authenticator

	apiKeyService, err := apikey.NewService(cfg.Store)
	if err != nil {
		return nil, err
	}
	apiKeyAuthenticator, err := apikey.NewAuthenticator(apiKeyService)
	if err != nil {
		return nil, err
	}
	result = append(result, apiKeyAuthenticator)

	oidcAuthenticator, err := authkitoidc.NewAuthenticator(
		cfg.Store,
		authkitoidc.WithHTTPClient(cfg.HTTPClient),
	)
	if err != nil {
		return nil, err
	}
	result = append(result, oidcAuthenticator)

	return result, nil
}

func displayName(prefix string, identity authkit.Identity) string {
	subject := strings.TrimSpace(identity.Subject)
	if subject == "" {
		return prefix
	}

	return prefix + ":" + subject
}

func principalAttributes(identity authkit.Identity) map[string]any {
	return map[string]any{
		"provider": identity.Provider,
		"subject":  identity.Subject,
	}
}
