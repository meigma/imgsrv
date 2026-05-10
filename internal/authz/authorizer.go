package authz

import (
	"context"
	"slices"
	"strings"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
)

const unauthorizedContentWrite = "principal is not authorized for action " + ActionContentWrite

// Authorizer applies imgsrv-specific authorization policy after authkit authenticates callers.
type Authorizer struct {
	roles      authkit.Authorizer
	oidc       OIDCConfig
	githubOIDC GitHubOIDCConfig
}

// Can decides whether check.Principal can perform check.Action.
func (a *Authorizer) Can(ctx context.Context, check authkit.AuthorizationCheck) (authkit.Decision, error) {
	if check.Action != ActionContentWrite {
		return denied("unsupported action"), nil
	}

	provider := factString(check.Facts, factIdentityProvider)
	switch provider {
	case apikey.Provider:
		return a.authorizeAPIKey(ctx, check)
	default:
		identity := identityFromFacts(check.Facts)
		if githubIdentityCanWrite(identity, a.githubOIDC) {
			return authkit.Decision{Allowed: true}, nil
		}
		if oidcIdentityCanWrite(identity, a.oidc) {
			return authkit.Decision{Allowed: true}, nil
		}

		return denied(unauthorizedContentWrite), nil
	}
}

func (a *Authorizer) authorizeAPIKey(
	ctx context.Context,
	check authkit.AuthorizationCheck,
) (authkit.Decision, error) {
	if a.roles == nil {
		return denied(unauthorizedContentWrite), nil
	}

	decision, err := a.roles.Can(ctx, check)
	if err != nil {
		return authkit.Decision{}, err
	}
	if decision.Allowed {
		return decision, nil
	}

	return denied(unauthorizedContentWrite), nil
}

func oidcIdentityCanWrite(identity authkit.Identity, cfg OIDCConfig) bool {
	if !cfg.configured() || identity.Provider != cfg.IssuerURL || strings.TrimSpace(cfg.RequiredScope) == "" {
		return false
	}

	if len(identity.Claims) == 0 {
		return false
	}
	value, ok := (authkit.ClaimPath{claimScope}).Lookup(identity.Claims)
	if !ok {
		return false
	}

	return scopeContains(value, cfg.RequiredScope)
}

func githubIdentityCanWrite(identity authkit.Identity, cfg GitHubOIDCConfig) bool {
	if !cfg.configured() || identity.Provider != cfg.IssuerURL || identity.Subject != cfg.Subject {
		return false
	}

	if len(identity.Claims) == 0 {
		return false
	}

	return claimStringEquals(identity.Claims, authkit.ClaimPath{claimRepositoryID}, cfg.RepositoryID) &&
		claimStringEquals(identity.Claims, authkit.ClaimPath{claimWorkflowRef}, cfg.WorkflowRef)
}

func scopeContains(value any, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return false
	}

	switch typed := value.(type) {
	case string:
		return slices.Contains(strings.Fields(typed), required)
	case []string:
		return slices.Contains(typed, required)
	case []any:
		return slices.ContainsFunc(typed, func(item any) bool {
			itemString, ok := item.(string)

			return ok && itemString == required
		})
	default:
		return false
	}
}

func claimStringEquals(claims map[string]any, path authkit.ClaimPath, want string) bool {
	got, ok := path.Lookup(claims)
	if !ok {
		return false
	}
	gotString, ok := got.(string)

	return ok && gotString == want
}

func factString(facts authkit.Facts, key authkit.FactKey) string {
	value, ok := facts[key]
	if !ok {
		return ""
	}
	typed, ok := value.(string)
	if !ok {
		return ""
	}

	return typed
}

func factClaims(facts authkit.Facts) (map[string]any, bool) {
	value, ok := facts[factIdentityClaims]
	if !ok {
		return nil, false
	}
	typed, ok := value.(map[string]any)

	return typed, ok
}

func identityFromFacts(facts authkit.Facts) authkit.Identity {
	claims, _ := factClaims(facts)

	return authkit.Identity{
		Provider: factString(facts, factIdentityProvider),
		Subject:  factString(facts, factIdentitySubject),
		Claims:   claims,
	}
}

func denied(reason string) authkit.Decision {
	if strings.TrimSpace(reason) == "" {
		reason = unauthorizedContentWrite
	}

	return authkit.Decision{
		Allowed: false,
		Reason:  reason,
	}
}
