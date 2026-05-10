package authz

import (
	"context"
	"errors"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
)

type identityStore interface {
	authkit.PrincipalResolver
	authkit.IdentityProvisioner
}

type policyResolver struct {
	store      identityStore
	oidc       OIDCConfig
	githubOIDC GitHubOIDCConfig
}

func (r *policyResolver) ResolveIdentity(
	ctx context.Context,
	identity authkit.Identity,
) (*authkit.Principal, error) {
	principal, err := r.store.ResolveIdentity(ctx, identity)
	if err == nil {
		return principal, nil
	}
	if !errors.Is(err, authkit.ErrUnresolvedIdentity) {
		return nil, err
	}

	switch {
	case identity.Provider == apikey.Provider:
		return nil, err
	case r.oidc.configured() && identity.Provider == r.oidc.IssuerURL:
		req := principalRequest("oidc", authkit.PrincipalKindUser, identity)
		if oidcIdentityCanWrite(identity, r.oidc) {
			return r.provision(ctx, identity, req)
		}

		return transientPrincipal(identity, req), nil
	case r.githubOIDC.configured() && identity.Provider == r.githubOIDC.IssuerURL:
		req := principalRequest("github-actions", authkit.PrincipalKindService, identity)
		if githubIdentityCanWrite(identity, r.githubOIDC) {
			return r.provision(ctx, identity, req)
		}

		return transientPrincipal(identity, req), nil
	default:
		return nil, err
	}
}

func (r *policyResolver) provision(
	ctx context.Context,
	identity authkit.Identity,
	req authkit.CreatePrincipalRequest,
) (*authkit.Principal, error) {
	result, err := r.store.ProvisionIdentity(ctx, authkit.ProvisionIdentityRequest{
		Identity:  identity,
		Principal: req,
	})
	if err != nil {
		return nil, err
	}

	return &result.Principal, nil
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
