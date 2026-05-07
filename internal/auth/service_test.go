package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/auth"
	"github.com/meigma/imgsrv/internal/auth/mocks"
)

func TestServiceAuthenticateTokenLooksUpMarksTokenUsedAndReturnsPrincipal(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := auth.NewService(auth.ServiceConfig{Store: store})
	token := tokenFixture()
	hash, err := auth.HashToken("testtok.secret")
	require.NoError(t, err)

	store.EXPECT().
		LookupActiveToken(mock.Anything, auth.LookupActiveTokenParams{
			TokenPrefix: "testtok",
			TokenHash:   hash,
		}).
		Return(token, nil)
	usedToken := token
	store.EXPECT().
		MarkTokenUsed(mock.Anything, auth.MarkTokenUsedParams{ID: token.ID}).
		Return(usedToken, nil)

	got, err := service.AuthenticateToken(context.Background(), auth.AuthenticateTokenParams{
		Token: "testtok.secret",
	})

	require.NoError(t, err)
	assert.Equal(t, auth.Principal{
		Kind: auth.PrincipalKindAPIToken,
		ID:   usedToken.ID.String(),
		Actions: []auth.Action{
			auth.ActionContentWrite,
			auth.ActionAuthManage,
		},
	}, got)
	assert.True(t, got.HasAction(auth.ActionContentWrite))
	assert.True(t, got.HasAction(auth.ActionAuthManage))
}

func TestServiceAuthenticateTokenRejectsInvalidTokenBeforeStore(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := auth.NewService(auth.ServiceConfig{Store: store})

	got, err := service.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: "bad"},
	)

	require.ErrorIs(t, err, auth.ErrInvalid)
	assert.Equal(t, auth.Principal{}, got)
}

func TestServiceAuthenticateTokenReturnsLookupErrorWithoutMarkingUsed(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := auth.NewService(auth.ServiceConfig{Store: store})

	store.EXPECT().
		LookupActiveToken(mock.Anything, mock.Anything).
		Return(auth.Token{}, auth.ErrNotFound)

	got, err := service.AuthenticateToken(context.Background(), auth.AuthenticateTokenParams{
		Token: "testtok.secret",
	})

	require.ErrorIs(t, err, auth.ErrNotFound)
	assert.Equal(t, auth.Principal{}, got)
}

func TestServiceAuthenticateTokenFallsThroughToAdditionalAuthenticators(t *testing.T) {
	store := mocks.NewMockStore(t)
	want := auth.Principal{
		Kind:    auth.PrincipalKindOIDC,
		ID:      "https://issuer.example#subject",
		Actions: []auth.Action{auth.ActionContentWrite},
	}
	service := auth.NewService(auth.ServiceConfig{
		Store: store,
		Authenticators: []auth.Authenticator{
			staticAuthenticator{principal: want},
		},
	})

	store.EXPECT().
		LookupActiveToken(mock.Anything, mock.Anything).
		Return(auth.Token{}, auth.ErrNotFound)

	got, err := service.AuthenticateToken(context.Background(), auth.AuthenticateTokenParams{
		Token: "testtok.secret",
	})

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestServiceAuthenticateTokenReturnsMarkUsedError(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := auth.NewService(auth.ServiceConfig{Store: store})
	token := tokenFixture()

	store.EXPECT().
		LookupActiveToken(mock.Anything, mock.Anything).
		Return(token, nil)
	store.EXPECT().
		MarkTokenUsed(mock.Anything, auth.MarkTokenUsedParams{ID: token.ID}).
		Return(auth.Token{}, auth.ErrNotFound)

	got, err := service.AuthenticateToken(context.Background(), auth.AuthenticateTokenParams{
		Token: "testtok.secret",
	})

	require.ErrorIs(t, err, auth.ErrNotFound)
	assert.Equal(t, auth.Principal{}, got)
}

func TestServiceCreateTokenDelegatesToStore(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := auth.NewService(auth.ServiceConfig{Store: store})
	params := auth.CreateTokenParams{
		ID:          uuid.New(),
		Name:        "integration",
		TokenPrefix: "testtok",
		TokenHash:   "sha256:05a0186a1b7b54828e420d982da73204a37bc2d6994c4a853f7c0852420a2a88",
	}
	token := tokenFixture()

	store.EXPECT().
		CreateToken(mock.Anything, params).
		Return(token, nil)

	got, err := service.CreateToken(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, token, got)
}

func TestServiceReturnsUnavailableWhenStoreMissing(t *testing.T) {
	service := auth.NewService(auth.ServiceConfig{})

	_, err := service.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: "testtok.secret"},
	)
	require.EqualError(t, err, "auth service is not configured")

	_, err = service.CreateToken(context.Background(), auth.CreateTokenParams{})
	require.EqualError(t, err, "auth store is not configured")

	var nilService *auth.Service
	_, err = nilService.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: "testtok.secret"},
	)
	require.EqualError(t, err, "auth service is not configured")
}

func TestServicePreservesUnexpectedStoreErrors(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := auth.NewService(auth.ServiceConfig{Store: store})
	wantErr := errors.New("database offline")

	store.EXPECT().
		LookupActiveToken(mock.Anything, mock.Anything).
		Return(auth.Token{}, wantErr)

	_, err := service.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: "testtok.secret"},
	)

	require.ErrorIs(t, err, wantErr)
}

func tokenFixture() auth.Token {
	return auth.Token{
		ID:          uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Name:        "test",
		TokenPrefix: "testtok",
	}
}

type staticAuthenticator struct {
	principal auth.Principal
	err       error
}

func (authenticator staticAuthenticator) AuthenticateToken(
	context.Context,
	auth.AuthenticateTokenParams,
) (auth.Principal, error) {
	return authenticator.principal, authenticator.err
}
