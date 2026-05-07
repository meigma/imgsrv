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
	scope    string
	verifier *oidcVerifier
}

// NewOIDCAuthenticator discovers the configured issuer and constructs an OIDC authenticator.
func NewOIDCAuthenticator(ctx context.Context, config OIDCConfig) (*OIDCAuthenticator, error) {
	issuerURL := strings.TrimSpace(config.IssuerURL)
	audience := strings.TrimSpace(config.Audience)
	scope := strings.TrimSpace(config.RequiredScope)
	if issuerURL == "" || audience == "" || scope == "" {
		return nil, fmt.Errorf("%w: oidc issuer URL, audience, and required scope are required", ErrInvalid)
	}

	verifier, err := newOIDCVerifier(ctx, oidcVerifierConfig{
		IssuerURL: issuerURL,
		Audience:  audience,
		Now:       config.Now,
	})
	if err != nil {
		return nil, err
	}

	return &OIDCAuthenticator{
		scope:    scope,
		verifier: verifier,
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

	var claims oidcJWTClaims
	if err := authenticator.verifier.verifyClaims(ctx, params, &claims); err != nil {
		return Principal{}, err
	}
	if err := authenticator.verifier.validateCommonClaims(claims); err != nil {
		return Principal{}, err
	}

	var actions []Action
	if scopeContains(claims.Scope, authenticator.scope) {
		actions = append(actions, ActionContentWrite)
	}

	return Principal{
		Kind:    PrincipalKindOIDC,
		ID:      authenticator.verifier.principalID(claims.Subject),
		Actions: actions,
	}, nil
}

type oidcVerifierConfig struct {
	IssuerURL string
	Audience  string
	Now       func() time.Time
}

type oidcVerifier struct {
	issuerURL string
	audience  string
	verifier  *oidc.IDTokenVerifier
	now       func() time.Time
}

func newOIDCVerifier(ctx context.Context, config oidcVerifierConfig) (*oidcVerifier, error) {
	issuerURL := strings.TrimSpace(config.IssuerURL)
	audience := strings.TrimSpace(config.Audience)
	if issuerURL == "" || audience == "" {
		return nil, fmt.Errorf("%w: oidc issuer URL and audience are required", ErrInvalid)
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

	return &oidcVerifier{
		issuerURL: issuerURL,
		audience:  audience,
		verifier: provider.VerifierContext(oidcCtx, &oidc.Config{
			ClientID: audience,
			Now:      now,
		}),
		now: now,
	}, nil
}

func (verifier *oidcVerifier) verifyClaims(
	ctx context.Context,
	params AuthenticateTokenParams,
	claims any,
) error {
	if verifier == nil || verifier.verifier == nil {
		return fmt.Errorf("%w: oidc verifier is not configured", ErrInvalid)
	}
	rawToken := strings.TrimSpace(params.Token)
	if rawToken == "" {
		return fmt.Errorf("%w: oidc token is required", ErrInvalid)
	}

	verifiedToken, err := verifier.verifier.Verify(ctx, rawToken)
	if err != nil {
		return fmt.Errorf("%w: oidc token verification failed", ErrInvalid)
	}
	if err := verifiedToken.Claims(claims); err != nil {
		return fmt.Errorf("%w: oidc token claims are invalid", ErrInvalid)
	}

	return nil
}

func (verifier *oidcVerifier) validateCommonClaims(claims oidcJWTClaims) error {
	if claims.Issuer != verifier.issuerURL {
		return fmt.Errorf("%w: oidc issuer mismatch", ErrInvalid)
	}
	if !slices.Contains(claims.Audience, verifier.audience) {
		return fmt.Errorf("%w: oidc audience mismatch", ErrInvalid)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return fmt.Errorf("%w: oidc subject is required", ErrInvalid)
	}

	nowUnix := verifier.now().Unix()
	if claims.ExpiresAt == 0 || nowUnix >= claims.ExpiresAt {
		return fmt.Errorf("%w: oidc token is expired", ErrInvalid)
	}
	if claims.NotBefore != nil && nowUnix < *claims.NotBefore {
		return fmt.Errorf("%w: oidc token is not yet valid", ErrInvalid)
	}

	return nil
}

func (verifier *oidcVerifier) principalID(subject string) string {
	return verifier.issuerURL + "#" + subject
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
