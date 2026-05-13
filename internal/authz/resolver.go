package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/meigma/authkit/provisioning"

	safelog "github.com/meigma/imgsrv/internal/logging"
)

type identityStore interface {
	authkit.PrincipalLister
	authkit.PrincipalResolver
	authkit.IdentityProvisioner
	authkit.ProvisioningRuleLister
}

type policyResolver struct {
	store  identityStore
	logger *slog.Logger
}

func (r *policyResolver) ResolveIdentity(
	ctx context.Context,
	identity authkit.Identity,
) (*authkit.Principal, error) {
	localIdentity := claimFingerprintIdentity(identity)
	principal, err := r.store.ResolveIdentity(ctx, localIdentity)
	if err == nil {
		r.logResolved(ctx, identity, principal, "linked")
		return principal, nil
	}
	if !errors.Is(err, authkit.ErrUnresolvedIdentity) {
		return nil, err
	}

	switch identity.Provider {
	case apikey.Provider:
		return nil, err
	default:
		return r.resolveProvisionableIdentity(ctx, localIdentity, identity)
	}
}

func (r *policyResolver) resolveProvisionableIdentity(
	ctx context.Context,
	localIdentity authkit.Identity,
	identity authkit.Identity,
) (*authkit.Principal, error) {
	prefix, kind := principalShape(identity)
	req := principalRequest(prefix, kind, identity)
	match, ok, err := r.managedMatch(ctx, identity)
	if err != nil {
		return nil, err
	}
	if !ok {
		principal := transientPrincipal(identity, req)
		r.logResolved(ctx, identity, principal, "transient")

		return principal, nil
	}

	principal, found, err := r.existingRulePrincipal(ctx, identity, match.ruleIDs)
	if err != nil {
		return nil, err
	}
	if found {
		r.logResolved(ctx, identity, principal, "existing_rule_principal")
		return principal, nil
	}

	req.Attributes["provisioning_rule_id"] = match.primaryRuleID
	principal, err = r.provision(ctx, localIdentity, req, match.roleIDs)
	if err != nil {
		return nil, err
	}
	r.logAutoProvisioned(ctx, identity, principal, match)

	return principal, nil
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

func (r *policyResolver) logAutoProvisioned(
	ctx context.Context,
	identity authkit.Identity,
	principal *authkit.Principal,
	match managedRuleMatch,
) {
	logger := r.logger
	if logger == nil {
		logger = safelog.Nop()
	}
	logger.InfoContext(
		ctx,
		"identity auto-provisioned",
		"operation",
		"auth.resolve_identity",
		"identity_provider",
		identity.Provider,
		"subject_hash",
		safelog.SubjectHash(identity.Provider, identity.Subject),
		"principal_id",
		principal.ID,
		"provisioning_rule_id",
		match.primaryRuleID,
		"rule_count",
		len(match.ruleIDs),
		"role_count",
		len(match.roleIDs),
	)
}

func (r *policyResolver) logResolved(
	ctx context.Context,
	identity authkit.Identity,
	principal *authkit.Principal,
	mode string,
) {
	if r == nil || r.logger == nil || principal == nil {
		return
	}
	r.logger.DebugContext(
		ctx,
		"identity resolved",
		"operation",
		"auth.resolve_identity",
		"resolution",
		mode,
		"identity_provider",
		identity.Provider,
		"subject_hash",
		safelog.SubjectHash(identity.Provider, identity.Subject),
		"principal_id",
		principal.ID,
	)
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
