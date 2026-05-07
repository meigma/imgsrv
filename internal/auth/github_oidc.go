package auth

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const defaultGitHubActionsOIDCIssuerURL = "https://token.actions.githubusercontent.com"

// GitHubActionsOIDCConfig configures GitHub Actions OIDC publisher authentication.
type GitHubActionsOIDCConfig struct {
	// IssuerURL is the OIDC issuer URL. Empty selects GitHub Actions' public issuer.
	IssuerURL string

	// Audience is the required token audience.
	Audience string

	// RepositoryID is the exact GitHub repository_id claim allowed to write content.
	RepositoryID string

	// WorkflowRef is the exact GitHub workflow_ref claim allowed to write content.
	WorkflowRef string

	// Subject is the exact GitHub OIDC sub claim allowed to write content.
	Subject string

	// Now returns the current time for token lifetime validation. Nil selects time.Now.
	Now func() time.Time
}

// GitHubActionsOIDCAuthenticator validates GitHub Actions OIDC job identity tokens.
type GitHubActionsOIDCAuthenticator struct {
	repositoryID string
	workflowRef  string
	subject      string
	verifier     *oidcVerifier
}

// NewGitHubActionsOIDCAuthenticator constructs a GitHub Actions OIDC authenticator.
func NewGitHubActionsOIDCAuthenticator(
	ctx context.Context,
	config GitHubActionsOIDCConfig,
) (*GitHubActionsOIDCAuthenticator, error) {
	issuerURL := strings.TrimSpace(config.IssuerURL)
	if issuerURL == "" {
		issuerURL = defaultGitHubActionsOIDCIssuerURL
	}
	audience := strings.TrimSpace(config.Audience)
	repositoryID := strings.TrimSpace(config.RepositoryID)
	workflowRef := strings.TrimSpace(config.WorkflowRef)
	subject := strings.TrimSpace(config.Subject)
	if audience == "" || repositoryID == "" || workflowRef == "" || subject == "" {
		return nil, fmt.Errorf(
			"%w: github oidc audience, repository id, workflow ref, and subject are required",
			ErrInvalid,
		)
	}

	verifier, err := newOIDCVerifier(ctx, oidcVerifierConfig{
		IssuerURL: issuerURL,
		Audience:  audience,
		Now:       config.Now,
	})
	if err != nil {
		return nil, err
	}

	return &GitHubActionsOIDCAuthenticator{
		repositoryID: repositoryID,
		workflowRef:  workflowRef,
		subject:      subject,
		verifier:     verifier,
	}, nil
}

// AuthenticateToken verifies a GitHub Actions OIDC token and applies static publisher policy.
func (authenticator *GitHubActionsOIDCAuthenticator) AuthenticateToken(
	ctx context.Context,
	params AuthenticateTokenParams,
) (Principal, error) {
	if authenticator == nil || authenticator.verifier == nil {
		return Principal{}, fmt.Errorf("%w: github oidc authenticator is not configured", ErrInvalid)
	}

	var claims githubActionsOIDCClaims
	if err := authenticator.verifier.verifyClaims(ctx, params, &claims); err != nil {
		return Principal{}, err
	}
	if err := authenticator.verifier.validateCommonClaims(claims.common()); err != nil {
		return Principal{}, err
	}
	if strings.TrimSpace(claims.RepositoryID) == "" {
		return Principal{}, fmt.Errorf("%w: github oidc repository_id is required", ErrInvalid)
	}
	if strings.TrimSpace(claims.WorkflowRef) == "" {
		return Principal{}, fmt.Errorf("%w: github oidc workflow_ref is required", ErrInvalid)
	}

	var actions []Action
	if claims.RepositoryID == authenticator.repositoryID &&
		claims.WorkflowRef == authenticator.workflowRef &&
		claims.Subject == authenticator.subject {
		actions = append(actions, ActionContentWrite)
	}

	return Principal{
		Kind:    PrincipalKindGitHubActions,
		ID:      authenticator.verifier.principalID(claims.Subject),
		Actions: actions,
	}, nil
}

type githubActionsOIDCClaims struct {
	Issuer       string        `json:"iss"`
	Subject      string        `json:"sub"`
	Audience     audienceClaim `json:"aud"`
	ExpiresAt    int64         `json:"exp"`
	NotBefore    *int64        `json:"nbf,omitempty"`
	RepositoryID string        `json:"repository_id"`
	WorkflowRef  string        `json:"workflow_ref"`
}

func (claims githubActionsOIDCClaims) common() oidcJWTClaims {
	return oidcJWTClaims{
		Issuer:    claims.Issuer,
		Subject:   claims.Subject,
		Audience:  claims.Audience,
		ExpiresAt: claims.ExpiresAt,
		NotBefore: claims.NotBefore,
	}
}
