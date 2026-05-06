//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authdomain "github.com/meigma/imgsrv/internal/auth"
)

func TestAuthTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	store := startCatalogIntegrationStore(t)
	authStore := store.Auth()
	rawToken := "testtok.secret"
	prefix, err := authdomain.ParseTokenPrefix(rawToken)
	require.NoError(t, err)
	hash, err := authdomain.HashToken(rawToken)
	require.NoError(t, err)

	created, err := authStore.CreateToken(ctx, authdomain.CreateTokenParams{
		ID:          uuid.New(),
		Name:        "integration token",
		TokenPrefix: prefix,
		TokenHash:   hash,
	})
	require.NoError(t, err)
	assert.Equal(t, prefix, created.TokenPrefix)
	assert.Nil(t, created.LastUsedAt)
	assert.Nil(t, created.RevokedAt)

	lookedUp, err := authStore.LookupActiveToken(ctx, authdomain.LookupActiveTokenParams{
		TokenPrefix: prefix,
		TokenHash:   hash,
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, lookedUp.ID)

	used, err := authStore.MarkTokenUsed(ctx, authdomain.MarkTokenUsedParams{ID: created.ID})
	require.NoError(t, err)
	require.NotNil(t, used.LastUsedAt)

	revoked, err := authStore.RevokeToken(ctx, authdomain.RevokeTokenParams{ID: created.ID})
	require.NoError(t, err)
	require.NotNil(t, revoked.RevokedAt)

	_, err = authStore.LookupActiveToken(ctx, authdomain.LookupActiveTokenParams{
		TokenPrefix: prefix,
		TokenHash:   hash,
	})
	assert.ErrorIs(t, err, authdomain.ErrNotFound)

	_, err = authStore.MarkTokenUsed(ctx, authdomain.MarkTokenUsedParams{ID: created.ID})
	assert.ErrorIs(t, err, authdomain.ErrNotFound)
}
