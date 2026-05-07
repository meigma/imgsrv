package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	defaultOIDCHTTPTimeout      = 10 * time.Second
	defaultOIDCDiscoveryTimeout = 10 * time.Second
)

// OIDCConfig configures generic OIDC JWT bearer-token authentication.
type OIDCConfig struct {
	// IssuerURL is the exact OIDC issuer URL expected in accepted tokens.
	IssuerURL string

	// Audience is the required token audience.
	Audience string

	// RequiredScope is the token scope required before granting content.write.
	RequiredScope string

	// Now returns the current time for token lifetime validation. Nil selects time.Now.
	Now func() time.Time
}

// OIDCAuthenticator validates signed JWT bearer tokens from one OIDC issuer.
type OIDCAuthenticator struct {
	issuerURL string
	audience  string
	scope     string
	verifier  *oidc.IDTokenVerifier
	now       func() time.Time
}

// NewOIDCAuthenticator discovers the configured issuer and constructs an OIDC authenticator.
func NewOIDCAuthenticator(ctx context.Context, config OIDCConfig) (*OIDCAuthenticator, error) {
	issuerURL := strings.TrimSpace(config.IssuerURL)
	audience := strings.TrimSpace(config.Audience)
	scope := strings.TrimSpace(config.RequiredScope)
	if issuerURL == "" || audience == "" || scope == "" {
		return nil, fmt.Errorf("%w: oidc issuer URL, audience, and required scope are required", ErrInvalid)
	}

	oidcHTTPClient := &http.Client{Timeout: defaultOIDCHTTPTimeout}
	oidcCtx := oidc.ClientContext(ctx, oidcHTTPClient)
	discoveryCtx, cancel := context.WithTimeout(oidcCtx, defaultOIDCDiscoveryTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(discoveryCtx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover oidc issuer %q: %w", issuerURL, err)
	}
	var claims oidcProviderClaims
	if err := provider.Claims(&claims); err != nil {
		return nil, fmt.Errorf("read oidc issuer metadata: %w", err)
	}
	jwksURI := strings.TrimSpace(claims.JWKSURI)
	if jwksURI == "" {
		return nil, fmt.Errorf("%w: oidc jwks_uri is required", ErrInvalid)
	}

	now := config.Now
	if now == nil {
		now = time.Now
	}
	verifier := provider.VerifierContext(oidcCtx, &oidc.Config{
		ClientID: audience,
		Now:      now,
	})

	return &OIDCAuthenticator{
		issuerURL: issuerURL,
		audience:  audience,
		scope:     scope,
		verifier:  verifier,
		now:       now,
	}, nil
}

// AuthenticateToken verifies a JWT access token and returns a content-write principal.
func (authenticator *OIDCAuthenticator) AuthenticateToken(
	ctx context.Context,
	params AuthenticateTokenParams,
) (Principal, error) {
	if authenticator == nil || authenticator.verifier == nil {
		return Principal{}, fmt.Errorf("%w: oidc authenticator is not configured", ErrInvalid)
	}
	rawToken := strings.TrimSpace(params.Token)
	if rawToken == "" {
		return Principal{}, fmt.Errorf("%w: oidc token is required", ErrInvalid)
	}

	verifiedToken, err := authenticator.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: oidc token verification failed", ErrInvalid)
	}

	var claims oidcJWTClaims
	if err := verifiedToken.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("%w: oidc token claims are invalid", ErrInvalid)
	}
	if claims.Issuer != authenticator.issuerURL {
		return Principal{}, fmt.Errorf("%w: oidc issuer mismatch", ErrInvalid)
	}
	if !slices.Contains(claims.Audience, authenticator.audience) {
		return Principal{}, fmt.Errorf("%w: oidc audience mismatch", ErrInvalid)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return Principal{}, fmt.Errorf("%w: oidc subject is required", ErrInvalid)
	}

	nowUnix := authenticator.now().Unix()
	if claims.ExpiresAt == 0 || nowUnix >= claims.ExpiresAt {
		return Principal{}, fmt.Errorf("%w: oidc token is expired", ErrInvalid)
	}
	if claims.NotBefore != nil && nowUnix < *claims.NotBefore {
		return Principal{}, fmt.Errorf("%w: oidc token is not yet valid", ErrInvalid)
	}

	var actions []Action
	if scopeContains(claims.Scope, authenticator.scope) {
		actions = append(actions, ActionContentWrite)
	}

	return Principal{
		Kind:    PrincipalKindOIDC,
		ID:      authenticator.issuerURL + "#" + claims.Subject,
		Actions: actions,
	}, nil
}

type oidcProviderClaims struct {
	JWKSURI string `json:"jwks_uri"`
}

type oidcJWTClaims struct {
	Issuer    string        `json:"iss"`
	Subject   string        `json:"sub"`
	Audience  audienceClaim `json:"aud"`
	ExpiresAt int64         `json:"exp"`
	NotBefore *int64        `json:"nbf,omitempty"`
	Scope     string        `json:"scope"`
}

type audienceClaim []string

// UnmarshalJSON accepts both JWT audience encodings: a single string or a string array.
func (audience *audienceClaim) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*audience = []string{single}
		return nil
	}

	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*audience = many

	return nil
}

func scopeContains(scopeList string, scope string) bool {
	return slices.Contains(strings.Fields(scopeList), scope)
}
