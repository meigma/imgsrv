package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/auth"
)

const (
	testGitHubOIDCAudience     = "imgsrv-github"
	testGitHubRepositoryID     = "123456789"
	testGitHubWorkflowRef      = "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main"
	testOtherGitHubWorkflowRef = "meigma/imgsrv/.github/workflows/ci.yml@refs/heads/main"
	testGitHubSubject          = "repo:meigma/imgsrv:ref:refs/heads/main"
	testPullRequestSubject     = "repo:meigma/imgsrv:pull_request"
)

func TestGitHubActionsOIDCAuthenticatorAuthenticatesTrustedWorkflow(t *testing.T) {
	now := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	issuer := newFakeOIDCIssuer(t, now)
	authenticator := newTestGitHubActionsOIDCAuthenticator(t, issuer, now)
	token := signGitHubActionsToken(t, issuer, nil)

	got, err := authenticator.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: token},
	)

	require.NoError(t, err)
	assert.Equal(t, auth.Principal{
		Kind:    auth.PrincipalKindGitHubActions,
		ID:      issuer.URL() + "#" + testGitHubSubject,
		Actions: []auth.Action{auth.ActionContentWrite},
	}, got)
	assert.True(t, got.HasAction(auth.ActionContentWrite))
	assert.False(t, got.HasAction(auth.ActionAuthManage))
}

func TestGitHubActionsOIDCAuthenticatorAuthenticatesUntrustedWorkflowWithoutActions(t *testing.T) {
	now := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	issuer := newFakeOIDCIssuer(t, now)
	authenticator := newTestGitHubActionsOIDCAuthenticator(t, issuer, now)

	tests := map[string]func(map[string]any){
		"wrong repository id": func(claims map[string]any) {
			claims["repository_id"] = "987654321"
		},
		"wrong workflow ref": func(claims map[string]any) {
			claims["workflow_ref"] = testOtherGitHubWorkflowRef
		},
		"wrong subject": func(claims map[string]any) {
			claims["sub"] = "repo:meigma/imgsrv:ref:refs/heads/release"
		},
		"pull request subject": func(claims map[string]any) {
			claims["sub"] = testPullRequestSubject
			claims["event_name"] = "pull_request_target"
		},
	}

	for name, patchClaims := range tests {
		t.Run(name, func(t *testing.T) {
			token := signGitHubActionsToken(t, issuer, patchClaims)

			got, err := authenticator.AuthenticateToken(
				context.Background(),
				auth.AuthenticateTokenParams{Token: token},
			)

			require.NoError(t, err)
			assert.Equal(t, auth.PrincipalKindGitHubActions, got.Kind)
			assert.False(t, got.HasAction(auth.ActionContentWrite))
			assert.False(t, got.HasAction(auth.ActionAuthManage))
		})
	}
}

func TestGitHubActionsOIDCAuthenticatorRejectsInvalidTokens(t *testing.T) {
	now := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	issuer := newFakeOIDCIssuer(t, now)
	authenticator := newTestGitHubActionsOIDCAuthenticator(t, issuer, now)

	tests := map[string]struct {
		token func(t *testing.T) string
	}{
		"wrong audience": {
			token: func(t *testing.T) string {
				return signGitHubActionsToken(t, issuer, func(claims map[string]any) {
					claims["aud"] = []string{"different-api"}
				})
			},
		},
		"bad signature": {
			token: func(t *testing.T) string {
				badKey, err := rsa.GenerateKey(rand.Reader, 2048)
				require.NoError(t, err)

				return issuer.SignTokenWithKey(t, badKey, "bad-key", githubActionsTokenClaimsPatch(nil))
			},
		},
		"expired": {
			token: func(t *testing.T) string {
				return signGitHubActionsToken(t, issuer, func(claims map[string]any) {
					claims["exp"] = now.Add(-time.Minute).Unix()
				})
			},
		},
		"not yet valid": {
			token: func(t *testing.T) string {
				return signGitHubActionsToken(t, issuer, func(claims map[string]any) {
					claims["nbf"] = now.Add(time.Minute).Unix()
				})
			},
		},
		"missing subject": {
			token: func(t *testing.T) string {
				return signGitHubActionsToken(t, issuer, func(claims map[string]any) {
					delete(claims, "sub")
				})
			},
		},
		"missing repository id": {
			token: func(t *testing.T) string {
				return signGitHubActionsToken(t, issuer, func(claims map[string]any) {
					delete(claims, "repository_id")
				})
			},
		},
		"malformed repository id": {
			token: func(t *testing.T) string {
				return signGitHubActionsToken(t, issuer, func(claims map[string]any) {
					claims["repository_id"] = 123456789
				})
			},
		},
		"missing workflow ref": {
			token: func(t *testing.T) string {
				return signGitHubActionsToken(t, issuer, func(claims map[string]any) {
					delete(claims, "workflow_ref")
				})
			},
		},
		"malformed": {
			token: func(_ *testing.T) string {
				return "not-a-jwt"
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := authenticator.AuthenticateToken(
				context.Background(),
				auth.AuthenticateTokenParams{Token: test.token(t)},
			)

			require.ErrorIs(t, err, auth.ErrInvalid)
			assert.Equal(t, auth.Principal{}, got)
		})
	}
}

func newTestGitHubActionsOIDCAuthenticator(
	t *testing.T,
	issuer *fakeOIDCIssuer,
	now time.Time,
) *auth.GitHubActionsOIDCAuthenticator {
	t.Helper()

	authenticator, err := auth.NewGitHubActionsOIDCAuthenticator(
		context.Background(),
		auth.GitHubActionsOIDCConfig{
			IssuerURL:    issuer.URL(),
			Audience:     testGitHubOIDCAudience,
			RepositoryID: testGitHubRepositoryID,
			WorkflowRef:  testGitHubWorkflowRef,
			Subject:      testGitHubSubject,
			Now: func() time.Time {
				return now
			},
		},
	)
	require.NoError(t, err)

	return authenticator
}

func signGitHubActionsToken(
	t *testing.T,
	issuer *fakeOIDCIssuer,
	patchClaims func(map[string]any),
) string {
	t.Helper()

	return issuer.SignToken(t, githubActionsTokenClaimsPatch(patchClaims))
}

func githubActionsTokenClaimsPatch(patchClaims func(map[string]any)) func(map[string]any) {
	return func(claims map[string]any) {
		claims["aud"] = []string{testGitHubOIDCAudience}
		claims["sub"] = testGitHubSubject
		claims["repository_id"] = testGitHubRepositoryID
		claims["workflow_ref"] = testGitHubWorkflowRef
		delete(claims, "scope")
		if patchClaims != nil {
			patchClaims(claims)
		}
	}
}
