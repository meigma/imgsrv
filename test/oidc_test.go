//go:build integration

package imgsrvtest_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	"github.com/meigma/imgsrv/internal/integration/testoidc"
	imgsrvtest "github.com/meigma/imgsrv/test"
)

func TestOIDCBearerTokenCanUseWriteFlow(t *testing.T) {
	issuer := testoidc.Start(t, time.Now().UTC())
	token := issuer.SignToken(t, nil)
	env := imgsrvtest.Start(
		t,
		imgsrvtest.WithOIDC(issuer.URL(), "imgsrv-api", "imgsrv.write"),
		imgsrvtest.WithBearerToken(token),
	)

	ctx := context.Background()
	image, err := env.Client(t).Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "oidc-public-flow",
	})

	require.NoError(t, err)
	assert.Equal(t, "oidc-public-flow", image.Name)

	anonymousClient, err := imgsrv.New(imgsrv.Options{
		BaseURL:    env.BaseURL(),
		HTTPClient: env.HTTPClient(),
	})
	require.NoError(t, err)

	_, err = anonymousClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "oidc-public-flow-anonymous",
	})
	assertProblemStatus(t, err, http.StatusUnauthorized)

	unscopedToken := issuer.SignToken(t, func(claims map[string]any) {
		claims["scope"] = "openid profile"
	})
	unscopedClient, err := imgsrv.New(imgsrv.Options{
		BaseURL:     env.BaseURL(),
		HTTPClient:  env.HTTPClient(),
		BearerToken: unscopedToken,
	})
	require.NoError(t, err)

	_, err = unscopedClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "oidc-public-flow-unscoped",
	})
	assertProblemStatus(t, err, http.StatusForbidden)
}

func assertProblemStatus(t testing.TB, err error, status int) {
	t.Helper()

	var problem *imgsrv.ProblemError
	require.ErrorAs(t, err, &problem)
	assert.Equal(t, status, problem.HTTPStatus)
}
