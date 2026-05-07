package auth

import (
	"context"
	"errors"
)

var errStoreUnavailable = errors.New("auth store is not configured")

// ServiceConfig configures an authentication service.
type ServiceConfig struct {
	// Store persists API-token state.
	Store Store
}

// Service coordinates API-token authentication.
type Service struct {
	// store persists API-token state.
	store Store
}

// NewService constructs an authentication service from config.
func NewService(config ServiceConfig) *Service {
	return &Service{store: config.Store}
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
	store, err := service.dependencies()
	if err != nil {
		return Principal{}, err
	}

	prefix, err := ParseTokenPrefix(params.Token)
	if err != nil {
		return Principal{}, err
	}
	tokenHash, err := HashToken(params.Token)
	if err != nil {
		return Principal{}, err
	}

	token, err := store.LookupActiveToken(ctx, LookupActiveTokenParams{
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
	})
	if err != nil {
		return Principal{}, err
	}

	usedToken, err := store.MarkTokenUsed(ctx, MarkTokenUsedParams{ID: token.ID})
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
