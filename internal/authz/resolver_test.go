package authz

import (
	"context"
	"testing"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyResolverDoesNotProvisionDeniedOIDCIdentity(t *testing.T) {
	store := &resolverStore{resolveErr: authkit.ErrUnresolvedIdentity}
	resolver := policyResolver{
		store: store,
		oidc: OIDCConfig{
			IssuerURL:     "https://issuer.example",
			RequiredScope: "imgsrv.write",
		},
	}

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

func TestPolicyResolverProvisionsAllowedOIDCIdentity(t *testing.T) {
	store := &resolverStore{resolveErr: authkit.ErrUnresolvedIdentity}
	resolver := policyResolver{
		store: store,
		oidc: OIDCConfig{
			IssuerURL:     "https://issuer.example",
			RequiredScope: "imgsrv.write",
		},
	}

	principal, err := resolver.ResolveIdentity(context.Background(), authkit.Identity{
		Provider: "https://issuer.example",
		Subject:  "subject-1",
		Claims: map[string]any{
			"scope": "openid profile imgsrv.write",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, store.provisionCalls)
	assert.Equal(t, "persisted-principal", principal.ID)
}

func TestPolicyResolverDoesNotProvisionDeniedGitHubIdentity(t *testing.T) {
	store := &resolverStore{resolveErr: authkit.ErrUnresolvedIdentity}
	resolver := policyResolver{
		store: store,
		githubOIDC: GitHubOIDCConfig{
			IssuerURL:    "https://token.actions.githubusercontent.com",
			RepositoryID: "123456789",
			WorkflowRef:  "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main",
			Subject:      "repo:meigma/imgsrv:ref:refs/heads/main",
		},
	}

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
	resolveErr     error
	provisionCalls int
}

func (s *resolverStore) ResolveIdentity(
	_ context.Context,
	_ authkit.Identity,
) (*authkit.Principal, error) {
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
