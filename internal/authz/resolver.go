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
	localIdentity := localResolutionIdentity(identity)
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
		if roleIDs, ok, roleErr := r.managedInitialRoleIDs(ctx, identity); roleErr != nil {
			return nil, roleErr
		} else if ok {
			return r.provision(ctx, localIdentity, req, roleIDs)
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

func (r *policyResolver) managedInitialRoleIDs(
	ctx context.Context,
	identity authkit.Identity,
) ([]string, bool, error) {
	rules, err := r.store.ListProvisioningRules(ctx)
	if err != nil {
		return nil, false, err
	}
	roleIDs := provisioning.MatchRules(identity, rules)
	if !slices.Contains(roleIDs, RoleContentWriter) {
		return nil, false, nil
	}

	return roleIDs, true, nil
}

func principalShape(identity authkit.Identity) (string, authkit.PrincipalKind) {
	if identity.Provider == DefaultGitHubOIDCIssuerURL {
		return "github-actions", authkit.PrincipalKindService
	}

	return "oidc", authkit.PrincipalKindUser
}

func localResolutionIdentity(identity authkit.Identity) authkit.Identity {
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
