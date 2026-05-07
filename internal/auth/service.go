package auth

import (
	"context"
	"errors"
)

var errStoreUnavailable = errors.New("auth store is not configured")
var errAuthUnavailable = errors.New("auth service is not configured")

// ServiceConfig configures an authentication service.
type ServiceConfig struct {
	// Store persists API-token state.
	Store Store

	// Authenticators are additional bearer-token authenticators tried after API tokens.
	Authenticators []Authenticator
}

// Service coordinates bearer-token authentication.
type Service struct {
	// store persists API-token state.
	store Store

	// authenticators validate bearer tokens in order.
	authenticators []Authenticator
}

// NewService constructs an authentication service from config.
func NewService(config ServiceConfig) *Service {
	authenticators := make([]Authenticator, 0, 1+len(config.Authenticators))
	if config.Store != nil {
		authenticators = append(authenticators, apiTokenAuthenticator{store: config.Store})
	}
	authenticators = append(authenticators, config.Authenticators...)

	return &Service{
		store:          config.Store,
		authenticators: authenticators,
	}
}

// CreateToken stores metadata for a pre-generated raw API token.
func (service *Service) CreateToken(ctx context.Context, params CreateTokenParams) (Token, error) {
	store, err := service.dependencies()
	if err != nil {
		return Token{}, err
	}

	return store.CreateToken(ctx, params)
}

// AuthenticateToken validates token, records successful use, and returns an API-token principal.
func (service *Service) AuthenticateToken(
	ctx context.Context,
	params AuthenticateTokenParams,
) (Principal, error) {
	if service == nil || len(service.authenticators) == 0 {
		return Principal{}, errAuthUnavailable
	}

	var authErr error
	for _, authenticator := range service.authenticators {
		principal, err := authenticator.AuthenticateToken(ctx, params)
		if err == nil {
			return principal, nil
		}
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
			authErr = err
			continue
		}

		return Principal{}, err
	}
	if authErr != nil {
		return Principal{}, authErr
	}

	return Principal{}, ErrNotFound
}

type apiTokenAuthenticator struct {
	store Store
}

// AuthenticateToken validates token, records successful use, and returns an API-token principal.
func (authenticator apiTokenAuthenticator) AuthenticateToken(
	ctx context.Context,
	params AuthenticateTokenParams,
) (Principal, error) {
	prefix, err := ParseTokenPrefix(params.Token)
	if err != nil {
		return Principal{}, err
	}
	tokenHash, err := HashToken(params.Token)
	if err != nil {
		return Principal{}, err
	}

	token, err := authenticator.store.LookupActiveToken(ctx, LookupActiveTokenParams{
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
	})
	if err != nil {
		return Principal{}, err
	}

	usedToken, err := authenticator.store.MarkTokenUsed(ctx, MarkTokenUsedParams{ID: token.ID})
	if err != nil {
		return Principal{}, err
	}

	return Principal{
		Kind: PrincipalKindAPIToken,
		ID:   usedToken.ID.String(),
		Actions: []Action{
			ActionContentWrite,
			ActionAuthManage,
		},
	}, nil
}

// dependencies returns configured service dependencies or a sentinel error.
func (service *Service) dependencies() (Store, error) {
	if service == nil || service.store == nil {
		return nil, errStoreUnavailable
	}

	return service.store, nil
}
