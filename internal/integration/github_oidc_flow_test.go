//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	"github.com/meigma/imgsrv/internal/integration/harness"
	"github.com/meigma/imgsrv/internal/integration/testoidc"
)

const (
	githubOIDCAudience     = "imgsrv-github"
	githubRepositoryID     = "123456789"
	githubWorkflowRef      = "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main"
	otherGitHubWorkflowRef = "meigma/imgsrv/.github/workflows/ci.yml@refs/heads/main"
	githubOIDCSubject      = "repo:meigma/imgsrv:ref:refs/heads/main"
	pullRequestSubject     = "repo:meigma/imgsrv:pull_request"
)

func TestGitHubActionsOIDCWriteFlow(t *testing.T) {
	issuer := testoidc.Start(t, time.Now().UTC())
	env := harness.Start(t, harness.WithGitHubActionsOIDC(
		issuer.URL(),
		githubOIDCAudience,
		githubRepositoryID,
		githubWorkflowRef,
		githubOIDCSubject,
	))
	ctx := t.Context()
	trustedClient := newBearerClient(t, env, issuer.SignToken(t, githubActionsClaims(nil)))

	image, err := trustedClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "github-oidc-flow",
	})

	require.NoError(t, err)
	assert.Equal(t, "github-oidc-flow", image.Name)

	untrustedClient := newBearerClient(t, env, issuer.SignToken(t, githubActionsClaims(
		func(claims map[string]any) {
			claims["workflow_ref"] = otherGitHubWorkflowRef
		},
	)))

	_, err = untrustedClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "github-oidc-flow-untrusted",
	})
	assertProblemStatus(t, err, http.StatusForbidden)

	prClient := newBearerClient(t, env, issuer.SignToken(t, githubActionsClaims(
		func(claims map[string]any) {
			claims["sub"] = pullRequestSubject
			claims["event_name"] = "pull_request_target"
		},
	)))

	_, err = prClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "github-oidc-flow-pr",
	})
	assertProblemStatus(t, err, http.StatusForbidden)
}

func newBearerClient(t testing.TB, env *harness.Env, token string) *imgsrv.Client {
	t.Helper()

	client, err := imgsrv.New(imgsrv.Options{
		BaseURL:     env.BaseURL(),
		HTTPClient:  env.HTTPClient(),
		BearerToken: token,
	})
	require.NoError(t, err)

	return client
}

func githubActionsClaims(patchClaims func(map[string]any)) func(map[string]any) {
	return func(claims map[string]any) {
		claims["aud"] = []string{githubOIDCAudience}
		claims["sub"] = githubOIDCSubject
		claims["repository_id"] = githubRepositoryID
		claims["workflow_ref"] = githubWorkflowRef
		delete(claims, "scope")
		if patchClaims != nil {
			patchClaims(claims)
		}
	}
}
