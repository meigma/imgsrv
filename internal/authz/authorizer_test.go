package authz

import (
	"context"
	"testing"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizerDelegatesAPITokensToLocalRoles(t *testing.T) {
	roles := &recordingAuthorizer{decision: authkit.Decision{Allowed: true}}
	authorizer := Authorizer{roles: roles}

	decision, err := authorizer.Can(context.Background(), authkit.AuthorizationCheck{
		Action: ActionContentWrite,
		Facts: authkit.Facts{
			factIdentityProvider: apikey.Provider,
		},
	})

	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.True(t, roles.called)
}

func TestAuthorizerDelegatesPersistedOIDCPrincipalsToLocalRoles(t *testing.T) {
	roles := &recordingAuthorizer{decision: authkit.Decision{Allowed: true}}
	authorizer := Authorizer{roles: roles}

	decision, err := authorizer.Can(context.Background(), authkit.AuthorizationCheck{
		Principal: authkit.Principal{
			ID:   "principal-1",
			Kind: authkit.PrincipalKindService,
		},
		Action: ActionContentWrite,
		Facts: authkit.Facts{
			factIdentityProvider: "https://issuer.example",
		},
	})

	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.True(t, roles.called)
}

func TestAuthorizerDeniesTransientOIDCPrincipalsWithoutLocalRoles(t *testing.T) {
	roles := &recordingAuthorizer{decision: authkit.Decision{Allowed: true}}
	authorizer := Authorizer{roles: roles}

	decision, err := authorizer.Can(context.Background(), authkit.AuthorizationCheck{
		Principal: authkit.Principal{
			ID:   "transient:https://issuer.example:subject-1",
			Kind: authkit.PrincipalKindUser,
			Attributes: map[string]any{
				"transient": true,
			},
		},
		Action: ActionContentWrite,
		Facts: authkit.Facts{
			factIdentityProvider: "https://issuer.example",
		},
	})

	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.False(t, roles.called)
}

type recordingAuthorizer struct {
	decision authkit.Decision
	called   bool
}

func (a *recordingAuthorizer) Can(
	_ context.Context,
	_ authkit.AuthorizationCheck,
) (authkit.Decision, error) {
	a.called = true

	return a.decision, nil
}
