package authz

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/meigma/authkit/httpauth"
	authkitoidc "github.com/meigma/authkit/oidc"
	"github.com/meigma/authkit/roleauth"
)

const (
	// DefaultGitHubOIDCIssuerURL is GitHub Actions' public OIDC issuer.
	DefaultGitHubOIDCIssuerURL = "https://token.actions.githubusercontent.com"

	factIdentityProvider authkit.FactKey = "identity.provider"
	factIdentitySubject  authkit.FactKey = "identity.subject"
	factIdentityClaims   authkit.FactKey = "identity.claims"

	claimScope        = "scope"
	claimRepositoryID = "repository_id"
	claimWorkflowRef  = "workflow_ref"
)

// Store is the authkit storage contract imgsrv needs at runtime.
type Store interface {
	apikey.TokenStore
	authkitoidc.ProviderSource
	authkit.PrincipalResolver
	authkit.IdentityProvisioner
	authkit.PrincipalActionResolver
	authkit.ProvisioningRuleLister
}

// Config configures authkit HTTP middleware for imgsrv.
type Config struct {
	Store Store

	OIDC       OIDCConfig
	GitHubOIDC GitHubOIDCConfig

	HTTPClient    *http.Client
	ErrorRenderer httpauth.ErrorRenderer
}

// OIDCConfig configures generic OIDC publisher tokens.
type OIDCConfig struct {
	IssuerURL     string
	Audience      string
	RequiredScope string
}

// GitHubOIDCConfig configures GitHub Actions OIDC publisher tokens.
type GitHubOIDCConfig struct {
	IssuerURL    string
	Audience     string
	RepositoryID string
	WorkflowRef  string
	Subject      string
}

// NewMiddleware builds the authkit HTTP middleware used by protected imgsrv routes.
func NewMiddleware(ctx context.Context, cfg Config) (*httpauth.Middleware, error) {
	if cfg.Store == nil {
		return nil, errors.New("authz: authkit store is required")
	}

	authenticators, err := authenticators(ctx, cfg)
	if err != nil {
		return nil, err
	}
	roleAuthorizer, err := roleauth.NewAuthorizer(cfg.Store)
	if err != nil {
		return nil, err
	}
	githubPolicy := cfg.GitHubOIDC
	if githubPolicy.configured() {
		githubPolicy = githubPolicy.withDefaultIssuer()
	}
	policy := Authorizer{
		roles:      roleAuthorizer,
		oidc:       cfg.OIDC,
		githubOIDC: githubPolicy,
	}
	resolver := policyResolver{
		store:      cfg.Store,
		oidc:       cfg.OIDC,
		githubOIDC: githubPolicy,
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

func authenticators(ctx context.Context, cfg Config) ([]authkit.Authenticator, error) {
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

	var providers []authkitoidc.Provider
	if cfg.OIDC.configured() {
		provider, discoverErr := discoverProvider(
			ctx,
			cfg.HTTPClient,
			cfg.OIDC.IssuerURL,
			[]string{cfg.OIDC.Audience},
			[]authkit.ClaimPath{{claimScope}},
		)
		if discoverErr != nil {
			return nil, discoverErr
		}
		providers = append(providers, provider)
	}
	if cfg.GitHubOIDC.configured() {
		github := cfg.GitHubOIDC.withDefaultIssuer()
		provider, discoverErr := discoverProvider(
			ctx,
			cfg.HTTPClient,
			github.IssuerURL,
			[]string{github.Audience},
			[]authkit.ClaimPath{{claimRepositoryID}, {claimWorkflowRef}},
		)
		if discoverErr != nil {
			return nil, discoverErr
		}
		providers = append(providers, provider)
	}
	var staticSource authkitoidc.ProviderSource
	if len(providers) > 0 {
		static, staticErr := authkitoidc.NewStaticProviderSource(mergeProviders(providers)...)
		if staticErr != nil {
			return nil, staticErr
		}
		staticSource = static
	}
	oidcAuthenticator, err := authkitoidc.NewAuthenticator(
		providerSource{dynamic: cfg.Store, static: staticSource},
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

func (c OIDCConfig) configured() bool {
	return strings.TrimSpace(c.IssuerURL) != "" ||
		strings.TrimSpace(c.Audience) != "" ||
		strings.TrimSpace(c.RequiredScope) != ""
}

func (c GitHubOIDCConfig) configured() bool {
	return strings.TrimSpace(c.IssuerURL) != "" ||
		strings.TrimSpace(c.Audience) != "" ||
		strings.TrimSpace(c.RepositoryID) != "" ||
		strings.TrimSpace(c.WorkflowRef) != "" ||
		strings.TrimSpace(c.Subject) != ""
}

func (c GitHubOIDCConfig) withDefaultIssuer() GitHubOIDCConfig {
	if strings.TrimSpace(c.IssuerURL) == "" {
		c.IssuerURL = DefaultGitHubOIDCIssuerURL
	}

	return c
}
