package authz

import (
	"testing"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestManagedClaimPathsIncludesAudienceOnce(t *testing.T) {
	assert.Equal(t, []authkit.ClaimPath{
		{"repository_id"},
		{managedAudienceClaim},
	}, managedClaimPaths([]string{"repository_id"}))

	assert.Equal(t, []authkit.ClaimPath{
		{managedAudienceClaim},
	}, managedClaimPaths([]string{managedAudienceClaim}))
}
