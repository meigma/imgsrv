//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/meigma/authkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	"github.com/meigma/imgsrv/internal/integration/harness"
	"github.com/meigma/imgsrv/internal/integration/testoidc"
)

const (
	managedGitHubSubject = "repo:meigma/imgsrv:ref:refs/heads/main"
	deniedGitHubSubject  = "repo:meigma/imgsrv:ref:refs/heads/dev"
)

func TestManagedOIDCProvisioningRulesAuthorizePublishers(t *testing.T) {
	issuer := testoidc.Start(t, time.Now().UTC())
	env := harness.Start(
		t,
		harness.WithAPIToken(),
		harness.WithOIDCHTTPClient(issuer.HTTPClient()),
	)
	ctx := t.Context()
	adminClient := newBearerClient(t, env, env.APIToken())

	_, err := adminClient.Auth().CreateOIDCProvisioningRule(ctx, imgsrv.SaveOIDCProvisioningRuleRequest{
		ID:              "bad-rule",
		DisplayName:     "Bad rule",
		IssuerURL:       issuer.URL(),
		Audience:        githubOIDCAudience,
		ForwardedClaims: []string{"repository_id"},
		Condition:       "claims.repository_id",
	})
	assertProblemStatus(t, err, http.StatusBadRequest)

	githubRule, err := adminClient.Auth().CreateOIDCProvisioningRule(
		ctx,
		imgsrv.SaveOIDCProvisioningRuleRequest{
			ID:              "github-main-publisher",
			DisplayName:     "GitHub main publisher",
			IssuerURL:       issuer.URL(),
			Audience:        githubOIDCAudience,
			ForwardedClaims: []string{"repository_id", "workflow_ref"},
			Condition: `identity.subject == "` + managedGitHubSubject + `" &&
				claims.repository_id == "` + githubRepositoryID + `" &&
				claims.workflow_ref == "` + githubWorkflowRef + `"`,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "github-main-publisher", githubRule.ID)
	assert.Equal(t, []string{"content-writer"}, githubRule.AssignRoleIDs)

	scopeRule, err := adminClient.Auth().CreateOIDCProvisioningRule(
		ctx,
		imgsrv.SaveOIDCProvisioningRuleRequest{
			ID:              "scope-group-publisher",
			DisplayName:     "Scope and group publisher",
			IssuerURL:       issuer.URL(),
			Audience:        "imgsrv-api",
			ForwardedClaims: []string{"scope", "groups"},
			Condition:       `hasToken(claims.scope, "imgsrv.write") && hasAny(claims.groups, ["publishers"])`,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "scope-group-publisher", scopeRule.ID)

	wrongAudienceClient := newBearerClient(t, env, issuer.SignToken(t, githubActionsClaims(func(claims map[string]any) {
		claims["sub"] = managedGitHubSubject
		claims["aud"] = []string{"imgsrv-api"}
	})))
	_, err = wrongAudienceClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-github-oidc-wrong-audience",
	})
	assertProblemStatus(t, err, http.StatusForbidden)
	_, err = env.Store().Authkit().ResolveIdentity(ctx, authkit.Identity{
		Provider: issuer.URL(),
		Subject:  managedGitHubSubject,
	})
	require.True(t, errors.Is(err, authkit.ErrUnresolvedIdentity), "got %v", err)

	trustedGitHubClient := newBearerClient(t, env, issuer.SignToken(t, githubActionsClaims(func(claims map[string]any) {
		claims["sub"] = managedGitHubSubject
	})))
	image, err := trustedGitHubClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-github-oidc-flow",
	})
	require.NoError(t, err)
	assert.Equal(t, "managed-github-oidc-flow", image.Name)

	principal := requirePrincipalBySubject(t, adminClient, ctx, managedGitHubSubject)
	assert.NotEmpty(t, principal.ID)

	deniedClient := newBearerClient(t, env, issuer.SignToken(t, githubActionsClaims(func(claims map[string]any) {
		claims["sub"] = deniedGitHubSubject
		claims["workflow_ref"] = otherGitHubWorkflowRef
	})))
	_, err = deniedClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-github-oidc-denied",
	})
	assertProblemStatus(t, err, http.StatusForbidden)
	_, err = env.Store().Authkit().ResolveIdentity(ctx, authkit.Identity{
		Provider: issuer.URL(),
		Subject:  deniedGitHubSubject,
	})
	require.True(t, errors.Is(err, authkit.ErrUnresolvedIdentity), "got %v", err)

	scopeClient := newBearerClient(t, env, issuer.SignToken(t, func(claims map[string]any) {
		claims["groups"] = []string{"publishers"}
	}))
	image, err = scopeClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-scope-oidc-flow",
	})
	require.NoError(t, err)
	assert.Equal(t, "managed-scope-oidc-flow", image.Name)

	scopePrincipal := requirePrincipalBySubject(t, adminClient, ctx, "subject-1")

	_, err = adminClient.Auth().UpdateOIDCProvisioningRule(
		ctx,
		scopeRule.ID,
		imgsrv.SaveOIDCProvisioningRuleRequest{
			DisplayName:     scopeRule.DisplayName,
			IssuerURL:       issuer.URL(),
			Audience:        "imgsrv-api",
			ForwardedClaims: []string{"scope", "groups"},
			Condition:       `hasToken(claims.scope, "imgsrv.write") && hasAny(claims.groups, ["publishers"])`,
			Enabled:         boolPtr(false),
		},
	)
	require.NoError(t, err)

	futureDisabledClient := newBearerClient(t, env, issuer.SignToken(t, func(claims map[string]any) {
		claims["sub"] = "subject-disabled"
		claims["groups"] = []string{"publishers"}
	}))
	_, err = futureDisabledClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-scope-disabled-future",
	})
	assertProblemStatus(t, err, http.StatusForbidden)

	image, err = scopeClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-scope-existing-after-disable",
	})
	require.NoError(t, err)
	assert.Equal(t, "managed-scope-existing-after-disable", image.Name)

	_, err = adminClient.Auth().UpdateOIDCProvisioningRule(
		ctx,
		scopeRule.ID,
		imgsrv.SaveOIDCProvisioningRuleRequest{
			DisplayName:     scopeRule.DisplayName,
			IssuerURL:       issuer.URL(),
			Audience:        "imgsrv-api",
			ForwardedClaims: []string{"scope", "groups"},
			Condition:       `hasToken(claims.scope, "imgsrv.write") && hasAny(claims.groups, ["publishers"])`,
			Enabled:         boolPtr(true),
		},
	)
	require.NoError(t, err)
	require.NoError(t, adminClient.Auth().DeleteOIDCProvisioningRule(ctx, scopeRule.ID))

	futureDeletedClient := newBearerClient(t, env, issuer.SignToken(t, func(claims map[string]any) {
		claims["sub"] = "subject-deleted"
		claims["groups"] = []string{"publishers"}
	}))
	_, err = futureDeletedClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-scope-deleted-future",
	})
	assertProblemStatus(t, err, http.StatusForbidden)

	require.NoError(t, adminClient.Auth().UnassignPrincipalRole(ctx, scopePrincipal.ID, "content-writer"))
	_, err = scopeClient.Catalog().CreateImage(ctx, imgsrv.CreateImageRequest{
		Name: "managed-scope-existing-after-unassign",
	})
	assertProblemStatus(t, err, http.StatusForbidden)
}

func boolPtr(value bool) *bool {
	return &value
}

func requirePrincipalBySubject(
	t testing.TB,
	client *imgsrv.Client,
	ctx context.Context,
	subject string,
) imgsrv.Principal {
	t.Helper()

	principals, err := client.Auth().ListPrincipals(ctx)
	require.NoError(t, err)
	for _, principal := range principals {
		if got, _ := principal.Attributes["subject"].(string); got == subject {
			return principal
		}
	}

	require.Failf(t, "principal not found", "subject %q was not in %+v", subject, principals)
	return imgsrv.Principal{}
}
