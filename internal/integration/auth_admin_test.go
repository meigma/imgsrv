//go:build integration

package integration

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	"github.com/meigma/imgsrv/internal/authz"
	"github.com/meigma/imgsrv/internal/integration/harness"
)

func TestFirstStartBootstrapTokenIsPrintedOnce(t *testing.T) {
	var output bytes.Buffer
	env := harness.Start(t, harness.WithBootstrapOutput(&output))
	ctx := t.Context()

	text := output.String()
	require.Contains(t, text, "imgsrv bootstrap auth token")
	require.Contains(t, text, "expires_at:")
	token := requireBootstrapToken(t, text)

	adminClient := newBearerClient(t, env, token)
	created, err := adminClient.Auth().CreatePrincipal(ctx, imgsrv.CreatePrincipalRequest{
		Kind:        "service",
		DisplayName: "publisher",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)

	principals, err := adminClient.Auth().ListPrincipals(ctx)
	require.NoError(t, err)
	var bootstrap *imgsrv.Principal
	for i := range principals {
		if principals[i].DisplayName == "bootstrap auth manager" {
			bootstrap = &principals[i]
			break
		}
	}
	require.NotNil(t, bootstrap)
	assert.Equal(t, []string{"auth-manager"}, bootstrap.RoleIDs)

	var secondOutput bytes.Buffer
	require.NoError(t, authz.EnsureBootstrapAdmin(ctx, authz.BootstrapConfig{
		Store:  env.Store().Authkit(),
		Output: &secondOutput,
	}))
	assert.Empty(t, secondOutput.String())
}

func TestAuthAdminAPIManagesPrincipalsRolesAndTokens(t *testing.T) {
	env := harness.Start(t, harness.WithAPIToken())
	ctx := t.Context()
	adminClient := newBearerClient(t, env, env.APIToken())

	principal, err := adminClient.Auth().CreatePrincipal(ctx, imgsrv.CreatePrincipalRequest{
		Kind:        "service",
		DisplayName: "publisher",
	})
	require.NoError(t, err)
	require.NotEmpty(t, principal.ID)

	roles, err := adminClient.Auth().ListRoles(ctx)
	require.NoError(t, err)
	require.Len(t, roles, 2)
	require.NoError(t, adminClient.Auth().AssignPrincipalRole(ctx, principal.ID, "content-writer"))

	issued, err := adminClient.Auth().IssueAPIToken(ctx, principal.ID, imgsrv.IssueAPITokenRequest{
		Name:      "publisher token",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	require.NotEmpty(t, issued.Plaintext)

	publisherClient := newBearerClient(t, env, issued.Plaintext)
	image, err := publisherClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "admin-api-token-flow",
	})
	require.NoError(t, err)
	assert.Equal(t, "admin-api-token-flow", image.Name)

	tokens, err := adminClient.Auth().ListPrincipalAPITokens(ctx, principal.ID)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, issued.ID, tokens[0].ID)
	assert.Empty(t, tokens[0].Plaintext)

	require.NoError(t, adminClient.Auth().RevokeAPIToken(ctx, issued.ID))
	_, err = publisherClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "admin-api-token-revoked",
	})
	assertProblemStatus(t, err, http.StatusUnauthorized)
}

func requireBootstrapToken(t testing.TB, output string) string {
	t.Helper()

	for line := range strings.SplitSeq(output, "\n") {
		token, ok := strings.CutPrefix(line, "token: ")
		if ok {
			return strings.TrimSpace(token)
		}
	}

	require.Fail(t, "bootstrap token not printed")
	return ""
}
