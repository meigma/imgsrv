package authz

import (
	"context"
	"errors"

	authkitoidc "github.com/meigma/authkit/oidc"
)

type providerSource struct {
	dynamic authkitoidc.ProviderSource
	static  authkitoidc.ProviderSource
}

func (s providerSource) FindProvider(ctx context.Context, issuer string) (authkitoidc.Provider, error) {
	if s.dynamic != nil {
		provider, err := s.dynamic.FindProvider(ctx, issuer)
		if err == nil {
			return provider, nil
		}
		if !errors.Is(err, authkitoidc.ErrProviderNotFound) {
			return authkitoidc.Provider{}, err
		}
	}
	if s.static != nil {
		return s.static.FindProvider(ctx, issuer)
	}

	return authkitoidc.Provider{}, authkitoidc.ErrProviderNotFound
}
