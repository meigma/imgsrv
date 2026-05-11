package authz

import (
	"context"
	"strings"
	"testing"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyResolverDoesNotProvisionDeniedOIDCIdentity(t *testing.T) {
	store := &resolverStore{resolveErr: authkit.ErrUnresolvedIdentity}
	resolver := policyResolver{store: store}

	principal, err := resolver.ResolveIdentity(context.Background(), authkit.Identity{
		Provider: "https://issuer.example",
		Subject:  "subject-1",
		Claims: map[string]any{
			"scope": "openid profile",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 0, store.provisionCalls)
	assert.Equal(t, authkit.PrincipalKindUser, principal.Kind)
	assert.Equal(t, true, principal.Attributes["transient"])
}

func TestPolicyResolverProvisionsManagedOIDCRuleWithInitialRoles(t *testing.T) {
	store := &resolverStore{
		resolveErr: authkit.ErrUnresolvedIdentity,
		rules: []authkit.ProvisioningRule{
			{
				ID:            "github-main-publisher",
				Provider:      "https://issuer.example",
				Condition:     `identity.subject == "subject-1" && claims.repository_id == "123456789"`,
				AssignRoleIDs: []string{RoleContentWriter},
				Enabled:       true,
			},
		},
	}
	resolver := policyResolver{store: store}

	principal, err := resolver.ResolveIdentity(context.Background(), authkit.Identity{
		Provider: "https://issuer.example",
		Subject:  "subject-1",
		Claims: map[string]any{
			"repository_id": "123456789",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, store.provisionCalls)
	assert.Equal(t, "persisted-principal", principal.ID)
	assert.Equal(t, []string{RoleContentWriter}, store.provisionInitialRoleIDs)
	require.NotEmpty(t, store.resolvedIdentities)
	assert.Contains(t, store.resolvedIdentities[0].Subject, "subject-1#claims:")
	assert.Contains(t, store.provisionIdentity.Subject, "subject-1#claims:")
	assert.Equal(t, "github-main-publisher", principal.Attributes["provisioning_rule_id"])
}

func TestPolicyResolverUsesExistingRulePrincipalForChangedMatchingClaims(t *testing.T) {
	store := &resolverStore{
		resolveErr: authkit.ErrUnresolvedIdentity,
		principals: []authkit.Principal{
			{
				ID:          "existing-principal",
				Kind:        authkit.PrincipalKindUser,
				DisplayName: "oidc:subject-1",
				Attributes: map[string]any{
					"provider":             "https://issuer.example",
					"subject":              "subject-1",
					"provisioning_rule_id": "scope-group-publisher",
				},
			},
		},
		rules: []authkit.ProvisioningRule{
			{
				ID:            "scope-group-publisher",
				Provider:      "https://issuer.example",
				Condition:     `hasAny(claims.groups, ["publishers"])`,
				AssignRoleIDs: []string{RoleContentWriter},
				Enabled:       true,
			},
		},
	}
	resolver := policyResolver{store: store}

	principal, err := resolver.ResolveIdentity(context.Background(), authkit.Identity{
		Provider: "https://issuer.example",
		Subject:  "subject-1",
		Claims: map[string]any{
			"groups": []string{"publishers", "admins"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "existing-principal", principal.ID)
	assert.Equal(t, 0, store.provisionCalls)
}

func TestPolicyResolverDoesNotProvisionUnmatchedGitHubIdentity(t *testing.T) {
	store := &resolverStore{resolveErr: authkit.ErrUnresolvedIdentity}
	resolver := policyResolver{store: store}

	principal, err := resolver.ResolveIdentity(context.Background(), authkit.Identity{
		Provider: "https://token.actions.githubusercontent.com",
		Subject:  "repo:meigma/imgsrv:ref:refs/heads/main",
		Claims: map[string]any{
			"repository_id": "123456789",
			"workflow_ref":  "meigma/imgsrv/.github/workflows/ci.yml@refs/heads/main",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 0, store.provisionCalls)
	assert.Equal(t, authkit.PrincipalKindService, principal.Kind)
	assert.Equal(t, true, principal.Attributes["transient"])
}

func TestPolicyResolverLeavesAPITokensUnprovisioned(t *testing.T) {
	store := &resolverStore{resolveErr: authkit.ErrUnresolvedIdentity}
	resolver := policyResolver{store: store}

	principal, err := resolver.ResolveIdentity(context.Background(), authkit.Identity{
		Provider: apikey.Provider,
		Subject:  "token-1",
	})

	require.ErrorIs(t, err, authkit.ErrUnresolvedIdentity)
	assert.Nil(t, principal)
	assert.Equal(t, 0, store.provisionCalls)
}

type resolverStore struct {
	resolveErr              error
	rules                   []authkit.ProvisioningRule
	principals              []authkit.Principal
	provisionCalls          int
	provisionInitialRoleIDs []string
	resolvedIdentities      []authkit.Identity
	provisionIdentity       authkit.Identity
}

func (s *resolverStore) ResolveIdentity(
	_ context.Context,
	identity authkit.Identity,
) (*authkit.Principal, error) {
	s.resolvedIdentities = append(s.resolvedIdentities, identity)
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}

	return &authkit.Principal{ID: "existing-principal"}, nil
}

func (s *resolverStore) ProvisionIdentity(
	_ context.Context,
	req authkit.ProvisionIdentityRequest,
) (authkit.ProvisionIdentityResult, error) {
	s.provisionCalls++
	s.provisionInitialRoleIDs = append([]string(nil), req.InitialRoleIDs...)
	s.provisionIdentity = req.Identity

	return authkit.ProvisionIdentityResult{
		Principal: authkit.Principal{
			ID:          "persisted-principal",
			Kind:        req.Principal.Kind,
			DisplayName: req.Principal.DisplayName,
			Attributes:  req.Principal.Attributes,
		},
		Created: true,
	}, nil
}

func (s *resolverStore) ListProvisioningRules(context.Context) ([]authkit.ProvisioningRule, error) {
	return append([]authkit.ProvisioningRule(nil), s.rules...), nil
}

func (s *resolverStore) ListPrincipals(context.Context) ([]authkit.Principal, error) {
	return append([]authkit.Principal(nil), s.principals...), nil
}

func TestClaimFingerprintIdentityUsesRawAPITokenSubject(t *testing.T) {
	identity := claimFingerprintIdentity(authkit.Identity{
		Provider: apikey.Provider,
		Subject:  "token-1",
		Claims: map[string]any{
			"scope": "ignored",
		},
	})

	assert.Equal(t, "token-1", identity.Subject)
}

func TestClaimFingerprintIdentityIncludesOIDCClaims(t *testing.T) {
	identity := claimFingerprintIdentity(authkit.Identity{
		Provider: "https://issuer.example",
		Subject:  "subject-1",
		Claims: map[string]any{
			"repository_id": "123456789",
			"workflow_ref":  "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main",
		},
	})

	assert.True(t, strings.HasPrefix(identity.Subject, "subject-1#claims:"))
}
