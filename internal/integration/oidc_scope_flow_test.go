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

func TestOIDCScopeBearerTokenCanUseWriteFlow(t *testing.T) {
	issuer := testoidc.Start(t, time.Now().UTC())
	token := issuer.SignToken(t, nil)
	env := startEnv(
		t,
		harness.WithAPIToken(),
		harness.WithOIDCHTTPClient(issuer.HTTPClient()),
	)

	ctx := t.Context()
	adminClient := newBearerClient(t, env, env.APIToken())
	_, err := adminClient.Auth().CreateOIDCProvisioningRule(ctx, imgsrv.SaveOIDCProvisioningRuleRequest{
		ID:              "scope-publisher",
		DisplayName:     "Scope publisher",
		IssuerURL:       issuer.URL(),
		Audience:        "imgsrv-api",
		ForwardedClaims: []string{"scope"},
		Condition:       `hasToken(claims.scope, "imgsrv.write")`,
	})
	require.NoError(t, err)

	oidcClient := newBearerClient(t, env, token)
	image, err := oidcClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "oidc-scope-flow",
	})

	require.NoError(t, err)
	assert.Equal(t, "oidc-scope-flow", image.Name)

	anonymousClient, err := imgsrv.New(imgsrv.Options{
		BaseURL:    env.BaseURL(),
		HTTPClient: env.HTTPClient(),
	})
	require.NoError(t, err)

	_, err = anonymousClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "oidc-scope-flow-anonymous",
	})
	assertProblemStatus(t, err, http.StatusUnauthorized)

	unscopedToken := issuer.SignToken(t, func(claims map[string]any) {
		claims["scope"] = "openid profile"
	})
	unscopedClient := newBearerClient(t, env, unscopedToken)

	_, err = unscopedClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "oidc-scope-flow-unscoped",
	})
	assertProblemStatus(t, err, http.StatusForbidden)
}
