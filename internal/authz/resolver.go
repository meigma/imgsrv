package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/meigma/authkit/provisioning"
)

type identityStore interface {
	authkit.PrincipalLister
	authkit.PrincipalResolver
	authkit.IdentityProvisioner
	authkit.ProvisioningRuleLister
}

type policyResolver struct {
	store identityStore
}

func (r *policyResolver) ResolveIdentity(
	ctx context.Context,
	identity authkit.Identity,
) (*authkit.Principal, error) {
	localIdentity := claimFingerprintIdentity(identity)
	principal, err := r.store.ResolveIdentity(ctx, localIdentity)
	if err == nil {
		return principal, nil
	}
	if !errors.Is(err, authkit.ErrUnresolvedIdentity) {
		return nil, err
	}

	switch identity.Provider {
	case apikey.Provider:
		return nil, err
	default:
		prefix, kind := principalShape(identity)
		req := principalRequest(prefix, kind, identity)
		if match, ok, roleErr := r.managedMatch(ctx, identity); roleErr != nil {
			return nil, roleErr
		} else if ok {
			if principal, found, findErr := r.existingRulePrincipal(ctx, identity, match.ruleIDs); findErr != nil {
				return nil, findErr
			} else if found {
				return principal, nil
			}
			req.Attributes["provisioning_rule_id"] = match.primaryRuleID

			return r.provision(ctx, localIdentity, req, match.roleIDs)
		}

		return transientPrincipal(identity, req), nil
	}
}

func (r *policyResolver) provision(
	ctx context.Context,
	identity authkit.Identity,
	req authkit.CreatePrincipalRequest,
	initialRoleIDs []string,
) (*authkit.Principal, error) {
	result, err := r.store.ProvisionIdentity(ctx, authkit.ProvisionIdentityRequest{
		Identity:       identity,
		Principal:      req,
		InitialRoleIDs: initialRoleIDs,
	})
	if err != nil {
		return nil, err
	}

	return &result.Principal, nil
}

type managedRuleMatch struct {
	primaryRuleID string
	ruleIDs       []string
	roleIDs       []string
}

func (r *policyResolver) managedMatch(
	ctx context.Context,
	identity authkit.Identity,
) (managedRuleMatch, bool, error) {
	rules, err := r.store.ListProvisioningRules(ctx)
	if err != nil {
		return managedRuleMatch{}, false, err
	}
	roleIDs := make([]string, 0)
	ruleIDs := make([]string, 0)
	seenRoles := map[string]struct{}{}
	for _, rule := range rules {
		matchedRoleIDs := provisioning.MatchRules(identity, []authkit.ProvisioningRule{rule})
		if !slices.Contains(matchedRoleIDs, RoleContentWriter) {
			continue
		}
		ruleIDs = append(ruleIDs, rule.ID)
		for _, roleID := range matchedRoleIDs {
			if _, ok := seenRoles[roleID]; ok {
				continue
			}
			seenRoles[roleID] = struct{}{}
			roleIDs = append(roleIDs, roleID)
		}
	}
	if !slices.Contains(roleIDs, RoleContentWriter) {
		return managedRuleMatch{}, false, nil
	}

	return managedRuleMatch{
		primaryRuleID: ruleIDs[0],
		ruleIDs:       ruleIDs,
		roleIDs:       roleIDs,
	}, true, nil
}

func (r *policyResolver) existingRulePrincipal(
	ctx context.Context,
	identity authkit.Identity,
	ruleIDs []string,
) (*authkit.Principal, bool, error) {
	principals, err := r.store.ListPrincipals(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, principal := range principals {
		if principal.Attributes["provider"] != identity.Provider {
			continue
		}
		if principal.Attributes["subject"] != identity.Subject {
			continue
		}
		ruleID, ok := principal.Attributes["provisioning_rule_id"].(string)
		if !ok || !slices.Contains(ruleIDs, ruleID) {
			continue
		}

		return &principal, true, nil
	}

	return nil, false, nil
}

func principalShape(identity authkit.Identity) (string, authkit.PrincipalKind) {
	if identity.Provider == DefaultGitHubOIDCIssuerURL {
		return "github-actions", authkit.PrincipalKindService
	}

	return "oidc", authkit.PrincipalKindUser
}

func claimFingerprintIdentity(identity authkit.Identity) authkit.Identity {
	if identity.Provider == apikey.Provider || len(identity.Claims) == 0 {
		return identity
	}
	encoded, err := json.Marshal(identity.Claims)
	if err != nil {
		return identity
	}

	sum := sha256.Sum256([]byte(identity.Subject + "\x00" + string(encoded)))
	identity.Subject = identity.Subject + "#claims:" + hex.EncodeToString(sum[:])

	return identity
}

func principalRequest(
	prefix string,
	kind authkit.PrincipalKind,
	identity authkit.Identity,
) authkit.CreatePrincipalRequest {
	return authkit.CreatePrincipalRequest{
		Kind:        kind,
		DisplayName: displayName(prefix, identity),
		Attributes:  principalAttributes(identity),
	}
}

func transientPrincipal(
	identity authkit.Identity,
	req authkit.CreatePrincipalRequest,
) *authkit.Principal {
	attributes := principalAttributes(identity)
	attributes["transient"] = true

	return &authkit.Principal{
		ID:          "transient:" + identity.Provider + ":" + identity.Subject,
		Kind:        req.Kind,
		DisplayName: req.DisplayName,
		Attributes:  attributes,
	}
}
