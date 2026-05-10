package authz

import (
	"context"
	"testing"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizerDelegatesAPITokensToLocalRoles(t *testing.T) {
	roles := &recordingAuthorizer{decision: authkit.Decision{Allowed: true}}
	authorizer := Authorizer{roles: roles}

	decision, err := authorizer.Can(context.Background(), authkit.AuthorizationCheck{
		Action: ActionContentWrite,
		Facts: authkit.Facts{
			factIdentityProvider: apikey.Provider,
		},
	})

	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.True(t, roles.called)
}

func TestAuthorizerChecksOIDCScope(t *testing.T) {
	tests := []struct {
		name    string
		scope   any
		allowed bool
	}{
		{name: "space separated string", scope: "openid profile imgsrv.write", allowed: true},
		{name: "string slice", scope: []string{"openid", "imgsrv.write"}, allowed: true},
		{name: "any slice", scope: []any{"openid", "imgsrv.write"}, allowed: true},
		{name: "missing required scope", scope: "openid profile"},
		{name: "unsupported scope type", scope: 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authorizer := Authorizer{
				oidc: OIDCConfig{
					IssuerURL:     "https://issuer.example",
					RequiredScope: "imgsrv.write",
				},
			}

			decision, err := authorizer.Can(context.Background(), authkit.AuthorizationCheck{
				Action: ActionContentWrite,
				Facts: authkit.Facts{
					factIdentityProvider: "https://issuer.example",
					factIdentityClaims: map[string]any{
						"scope": tt.scope,
					},
				},
			})

			require.NoError(t, err)
			assert.Equal(t, tt.allowed, decision.Allowed)
		})
	}
}

func TestAuthorizerChecksGitHubClaimsExactly(t *testing.T) {
	tests := []struct {
		name        string
		subject     string
		repository  string
		workflowRef string
		allowed     bool
	}{
		{
			name:        "exact match",
			subject:     "repo:meigma/imgsrv:ref:refs/heads/main",
			repository:  "123456789",
			workflowRef: "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main",
			allowed:     true,
		},
		{
			name:        "wrong subject",
			subject:     "repo:meigma/imgsrv:pull_request",
			repository:  "123456789",
			workflowRef: "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main",
		},
		{
			name:        "wrong workflow",
			subject:     "repo:meigma/imgsrv:ref:refs/heads/main",
			repository:  "123456789",
			workflowRef: "meigma/imgsrv/.github/workflows/ci.yml@refs/heads/main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authorizer := Authorizer{
				githubOIDC: GitHubOIDCConfig{
					IssuerURL:    "https://token.actions.githubusercontent.com",
					RepositoryID: "123456789",
					WorkflowRef:  "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main",
					Subject:      "repo:meigma/imgsrv:ref:refs/heads/main",
				},
			}

			decision, err := authorizer.Can(context.Background(), authkit.AuthorizationCheck{
				Action: ActionContentWrite,
				Facts: authkit.Facts{
					factIdentityProvider: "https://token.actions.githubusercontent.com",
					factIdentitySubject:  tt.subject,
					factIdentityClaims: map[string]any{
						"repository_id": tt.repository,
						"workflow_ref":  tt.workflowRef,
					},
				},
			})

			require.NoError(t, err)
			assert.Equal(t, tt.allowed, decision.Allowed)
		})
	}
}

type recordingAuthorizer struct {
	decision authkit.Decision
	called   bool
}

func (a *recordingAuthorizer) Can(
	_ context.Context,
	_ authkit.AuthorizationCheck,
) (authkit.Decision, error) {
	a.called = true

	return a.decision, nil
}
