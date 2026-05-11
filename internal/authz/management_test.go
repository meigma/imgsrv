package authz

import (
	"context"
	"testing"
	"time"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/provisioning"
	"github.com/meigma/authkit/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagementServiceCoreAuthAdministration(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	service := NewManagementService(ManagementConfig{Store: store})
	require.NoError(t, EnsureBuiltinRoles(ctx, store))

	roles, err := service.ListRoles(ctx)
	require.NoError(t, err)
	assert.Equal(t, []Role{
		{
			ID:          RoleContentWriter,
			DisplayName: "Content writer",
			Description: "Can write imgsrv content.",
			Actions:     []string{ActionContentWrite},
		},
		{
			ID:          RoleAuthManager,
			DisplayName: "Auth manager",
			Description: "Can manage imgsrv authentication policy.",
			Actions:     []string{ActionAuthManage},
		},
	}, roles)

	principal, err := service.CreatePrincipal(ctx, CreatePrincipalRequest{
		Kind:        authkit.PrincipalKindService,
		DisplayName: "publisher",
	})
	require.NoError(t, err)
	assert.Empty(t, principal.RoleIDs)

	require.NoError(t, service.AssignPrincipalRole(ctx, principal.ID, RoleContentWriter))
	found, err := service.FindPrincipal(ctx, principal.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{RoleContentWriter}, found.RoleIDs)

	listed, err := service.ListPrincipals(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, principal.ID, listed[0].ID)

	expiresAt := time.Now().Add(time.Hour)
	issued, err := service.IssueAPIToken(ctx, IssueAPITokenRequest{
		PrincipalID: principal.ID,
		Name:        "deploy",
		ExpiresAt:   expiresAt,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, issued.Plaintext)

	tokens, err := service.ListPrincipalAPITokens(ctx, principal.ID)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, issued.ID, tokens[0].ID)
	assert.Empty(t, tokens[0].RevokedAt)

	require.NoError(t, service.RevokeAPIToken(ctx, issued.ID))
	tokens, err = service.ListPrincipalAPITokens(ctx, principal.ID)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	require.NotNil(t, tokens[0].RevokedAt)

	require.NoError(t, service.UnassignPrincipalRole(ctx, principal.ID, RoleContentWriter))
	found, err = service.FindPrincipal(ctx, principal.ID)
	require.NoError(t, err)
	assert.Empty(t, found.RoleIDs)
}

func TestManagedRuleConditionBindsAudience(t *testing.T) {
	operatorCondition := "identity.subject == 'repo:meigma/imgsrv:ref:refs/heads/main' && claims.repository_id == '123'"
	condition, err := managedRuleCondition("imgsrv-github", operatorCondition)
	require.NoError(t, err)

	audience, parsedCondition, ok := splitManagedRuleCondition(condition)
	require.True(t, ok)
	assert.Equal(t, "imgsrv-github", audience)
	assert.Equal(t, operatorCondition, parsedCondition)

	rules := []authkit.ProvisioningRule{
		{
			Provider:      "https://issuer.example.com",
			Condition:     condition,
			AssignRoleIDs: []string{RoleContentWriter},
			Enabled:       true,
		},
	}

	wrongAudienceRoles := provisioning.MatchRules(authkit.Identity{
		Provider: "https://issuer.example.com",
		Subject:  "repo:meigma/imgsrv:ref:refs/heads/main",
		Claims: map[string]any{
			"aud":           []string{"imgsrv-api"},
			"repository_id": "123",
		},
	}, rules)
	assert.Empty(t, wrongAudienceRoles)

	matchingRoles := provisioning.MatchRules(authkit.Identity{
		Provider: "https://issuer.example.com",
		Subject:  "repo:meigma/imgsrv:ref:refs/heads/main",
		Claims: map[string]any{
			"aud":           []string{"imgsrv-github"},
			"repository_id": "123",
		},
	}, rules)
	assert.Equal(t, []string{RoleContentWriter}, matchingRoles)
}

func TestManagementServiceReconcilesOIDCProvisioningRulePrincipals(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	service := NewManagementService(ManagementConfig{Store: store})
	require.NoError(t, EnsureBuiltinRoles(ctx, store))

	target, err := service.CreatePrincipal(ctx, CreatePrincipalRequest{
		Kind:        authkit.PrincipalKindService,
		DisplayName: "rule publisher",
		Attributes: map[string]any{
			"provisioning_rule_id": "rule-1",
		},
	})
	require.NoError(t, err)
	require.NoError(t, service.AssignPrincipalRole(ctx, target.ID, RoleContentWriter))
	require.NoError(t, service.AssignPrincipalRole(ctx, target.ID, RoleAuthManager))

	otherRule, err := service.CreatePrincipal(ctx, CreatePrincipalRequest{
		Kind:        authkit.PrincipalKindService,
		DisplayName: "other publisher",
		Attributes: map[string]any{
			"provisioning_rule_id": "rule-2",
		},
	})
	require.NoError(t, err)
	require.NoError(t, service.AssignPrincipalRole(ctx, otherRule.ID, RoleContentWriter))

	preview, err := service.PreviewOIDCProvisioningRuleReconciliation(ctx, "rule-1")
	require.NoError(t, err)
	assert.False(t, preview.Applied)
	assert.Equal(t, "rule-1", preview.RuleID)
	assert.Equal(t, []string{RoleContentWriter}, preview.UnassignRoleIDs)
	require.Len(t, preview.Principals, 1)
	assert.Equal(t, target.ID, preview.Principals[0].ID)
	assert.ElementsMatch(t, []string{RoleAuthManager, RoleContentWriter}, preview.Principals[0].RoleIDs)

	applied, err := service.ReconcileOIDCProvisioningRule(ctx, "rule-1")
	require.NoError(t, err)
	assert.True(t, applied.Applied)
	require.Len(t, applied.Principals, 1)
	assert.Equal(t, target.ID, applied.Principals[0].ID)
	assert.Equal(t, []string{RoleAuthManager}, applied.Principals[0].RoleIDs)

	found, err := service.FindPrincipal(ctx, target.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{RoleAuthManager}, found.RoleIDs)
	foundOther, err := service.FindPrincipal(ctx, otherRule.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{RoleContentWriter}, foundOther.RoleIDs)

	reapplied, err := service.ReconcileOIDCProvisioningRule(ctx, "rule-1")
	require.NoError(t, err)
	assert.True(t, reapplied.Applied)
	assert.Empty(t, reapplied.Principals)
}

func TestManagedClaimPathsIncludesAudienceOnce(t *testing.T) {
	assert.Equal(t, []authkit.ClaimPath{
		{"repository_id"},
		{managedAudienceClaim},
	}, managedClaimPaths([]string{"repository_id"}))

	assert.Equal(t, []authkit.ClaimPath{
		{managedAudienceClaim},
	}, managedClaimPaths([]string{managedAudienceClaim}))
}
