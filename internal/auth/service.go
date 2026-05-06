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

// AuthenticateToken validates token, records successful use, and returns token metadata.
func (service *Service) AuthenticateToken(ctx context.Context, params AuthenticateTokenParams) (Token, error) {
	store, err := service.dependencies()
	if err != nil {
		return Token{}, err
	}

	prefix, err := ParseTokenPrefix(params.Token)
	if err != nil {
		return Token{}, err
	}
	tokenHash, err := HashToken(params.Token)
	if err != nil {
		return Token{}, err
	}

	token, err := store.LookupActiveToken(ctx, LookupActiveTokenParams{
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
	})
	if err != nil {
		return Token{}, err
	}

	return store.MarkTokenUsed(ctx, MarkTokenUsedParams{ID: token.ID})
}

// dependencies returns configured service dependencies or a sentinel error.
func (service *Service) dependencies() (Store, error) {
	if service == nil || service.store == nil {
		return nil, errStoreUnavailable
	}

	return service.store, nil
}
