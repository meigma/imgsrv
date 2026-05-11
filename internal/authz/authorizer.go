package authz

import (
	"context"
	"strings"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
)

const unauthorizedContentWrite = "principal is not authorized for action " + ActionContentWrite

// Authorizer applies imgsrv-specific authorization policy after authkit authenticates callers.
type Authorizer struct {
	roles authkit.Authorizer
}

// Can decides whether check.Principal can perform check.Action.
func (a *Authorizer) Can(ctx context.Context, check authkit.AuthorizationCheck) (authkit.Decision, error) {
	provider := factString(check.Facts, factIdentityProvider)
	if provider == apikey.Provider || (a.roles != nil && !isTransientPrincipal(check.Principal)) {
		decision, err := a.authorizeWithRoles(ctx, check)
		if err != nil {
			return authkit.Decision{}, err
		}
		if decision.Allowed {
			return decision, nil
		}
		if check.Action == ActionAuthManage {
			return denied("principal is not authorized for action " + ActionAuthManage), nil
		}
	}

	return denied("principal is not authorized for action " + check.Action), nil
}

func isTransientPrincipal(principal authkit.Principal) bool {
	transient, _ := principal.Attributes["transient"].(bool)

	return transient
}

func (a *Authorizer) authorizeWithRoles(
	ctx context.Context,
	check authkit.AuthorizationCheck,
) (authkit.Decision, error) {
	if a.roles == nil {
		return denied("principal is not authorized for action " + check.Action), nil
	}

	decision, err := a.roles.Can(ctx, check)
	if err != nil {
		return authkit.Decision{}, err
	}
	if decision.Allowed {
		return decision, nil
	}

	return denied("principal is not authorized for action " + check.Action), nil
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

func denied(reason string) authkit.Decision {
	if strings.TrimSpace(reason) == "" {
		reason = unauthorizedContentWrite
	}

	return authkit.Decision{
		Allowed: false,
		Reason:  reason,
	}
}
