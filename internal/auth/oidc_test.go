package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/auth"
)

func TestOIDCAuthenticatorAuthenticatesValidToken(t *testing.T) {
	now := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	issuer := newFakeOIDCIssuer(t, now)
	authenticator := newTestOIDCAuthenticator(t, issuer, now)
	token := issuer.SignToken(t, nil)

	got, err := authenticator.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: token},
	)

	require.NoError(t, err)
	assert.Equal(t, auth.Principal{
		Kind:    auth.PrincipalKindOIDC,
		ID:      issuer.URL() + "#subject-1",
		Actions: []auth.Action{auth.ActionContentWrite},
	}, got)
	assert.True(t, got.HasAction(auth.ActionContentWrite))
	assert.False(t, got.HasAction(auth.ActionAuthManage))
}

func TestOIDCAuthenticatorRejectsInvalidTokens(t *testing.T) {
	now := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	issuer := newFakeOIDCIssuer(t, now)
	authenticator := newTestOIDCAuthenticator(t, issuer, now)

	tests := map[string]struct {
		token func(t *testing.T) string
	}{
		"wrong issuer": {
			token: func(t *testing.T) string {
				return issuer.SignToken(t, func(claims map[string]any) {
					claims["iss"] = "https://issuer.example.invalid"
				})
			},
		},
		"wrong audience": {
			token: func(t *testing.T) string {
				return issuer.SignToken(t, func(claims map[string]any) {
					claims["aud"] = []string{"different-api"}
				})
			},
		},
		"expired": {
			token: func(t *testing.T) string {
				return issuer.SignToken(t, func(claims map[string]any) {
					claims["exp"] = now.Add(-time.Minute).Unix()
				})
			},
		},
		"not yet valid": {
			token: func(t *testing.T) string {
				return issuer.SignToken(t, func(claims map[string]any) {
					claims["nbf"] = now.Add(time.Minute).Unix()
				})
			},
		},
		"missing subject": {
			token: func(t *testing.T) string {
				return issuer.SignToken(t, func(claims map[string]any) {
					delete(claims, "sub")
				})
			},
		},
		"malformed": {
			token: func(_ *testing.T) string {
				return "not-a-jwt"
			},
		},
		"bad signature": {
			token: func(t *testing.T) string {
				badKey, err := rsa.GenerateKey(rand.Reader, 2048)
				require.NoError(t, err)

				return issuer.SignTokenWithKey(t, badKey, "bad-key", nil)
			},
		},
		"unsupported signing algorithm": {
			token: func(t *testing.T) string {
				return issuer.SignTokenWithAlgorithm(t, issuer.key, issuer.keyID, jose.PS256, nil)
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := authenticator.AuthenticateToken(
				context.Background(),
				auth.AuthenticateTokenParams{Token: test.token(t)},
			)

			require.ErrorIs(t, err, auth.ErrInvalid)
			assert.Equal(t, auth.Principal{}, got)
		})
	}
}

func TestOIDCAuthenticatorDoesNotGrantWriteWithoutRequiredScope(t *testing.T) {
	now := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	issuer := newFakeOIDCIssuer(t, now)
	authenticator := newTestOIDCAuthenticator(t, issuer, now)
	token := issuer.SignToken(t, func(claims map[string]any) {
		claims["scope"] = "openid profile"
	})

	got, err := authenticator.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: token},
	)

	require.NoError(t, err)
	assert.Equal(t, auth.PrincipalKindOIDC, got.Kind)
	assert.False(t, got.HasAction(auth.ActionContentWrite))
	assert.False(t, got.HasAction(auth.ActionAuthManage))
}

func TestOIDCAuthenticatorAcceptsStringAudienceClaim(t *testing.T) {
	now := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	issuer := newFakeOIDCIssuer(t, now)
	authenticator := newTestOIDCAuthenticator(t, issuer, now)
	token := issuer.SignToken(t, func(claims map[string]any) {
		claims["aud"] = "imgsrv-api"
	})

	got, err := authenticator.AuthenticateToken(
		context.Background(),
		auth.AuthenticateTokenParams{Token: token},
	)

	require.NoError(t, err)
	assert.Equal(t, auth.PrincipalKindOIDC, got.Kind)
}

func newTestOIDCAuthenticator(
	t *testing.T,
	issuer *fakeOIDCIssuer,
	now time.Time,
) *auth.OIDCAuthenticator {
	t.Helper()

	authenticator, err := auth.NewOIDCAuthenticator(context.Background(), auth.OIDCConfig{
		IssuerURL:     issuer.URL(),
		Audience:      "imgsrv-api",
		RequiredScope: "imgsrv.write",
		Now: func() time.Time {
			return now
		},
	})
	require.NoError(t, err)

	return authenticator
}

type fakeOIDCIssuer struct {
	server     *httptest.Server
	key        *rsa.PrivateKey
	keyID      string
	now        time.Time
	algorithms []string
}

func newFakeOIDCIssuer(t *testing.T, now time.Time) *fakeOIDCIssuer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	issuer := &fakeOIDCIssuer{
		key:        key,
		keyID:      "test-key",
		now:        now,
		algorithms: []string{string(jose.RS256)},
	}
	mux := http.NewServeMux()
	issuer.server = httptest.NewServer(mux)
	t.Cleanup(issuer.server.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"issuer":                                issuer.URL(),
			"jwks_uri":                              issuer.URL() + "/jwks",
			"id_token_signing_alg_values_supported": issuer.algorithms,
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"keys": []jose.JSONWebKey{
				{
					Key:       &issuer.key.PublicKey,
					KeyID:     issuer.keyID,
					Algorithm: string(jose.RS256),
					Use:       "sig",
				},
			},
		})
	})

	return issuer
}

func (issuer *fakeOIDCIssuer) URL() string {
	return issuer.server.URL
}

func (issuer *fakeOIDCIssuer) SignToken(
	t *testing.T,
	patchClaims func(map[string]any),
) string {
	t.Helper()

	return issuer.SignTokenWithKey(t, issuer.key, issuer.keyID, patchClaims)
}

func (issuer *fakeOIDCIssuer) SignTokenWithKey(
	t *testing.T,
	key *rsa.PrivateKey,
	keyID string,
	patchClaims func(map[string]any),
) string {
	t.Helper()

	return issuer.SignTokenWithAlgorithm(t, key, keyID, jose.RS256, patchClaims)
}

func (issuer *fakeOIDCIssuer) SignTokenWithAlgorithm(
	t *testing.T,
	key *rsa.PrivateKey,
	keyID string,
	algorithm jose.SignatureAlgorithm,
	patchClaims func(map[string]any),
) string {
	t.Helper()

	claims := map[string]any{
		"iss":   issuer.URL(),
		"sub":   "subject-1",
		"aud":   []string{"imgsrv-api", "other-api"},
		"exp":   issuer.now.Add(5 * time.Minute).Unix(),
		"nbf":   issuer.now.Add(-1 * time.Minute).Unix(),
		"scope": "openid profile imgsrv.write",
	}
	if patchClaims != nil {
		patchClaims(claims)
	}
	payload, err := json.Marshal(claims)
	require.NoError(t, err)

	options := (&jose.SignerOptions{}).
		WithType(jose.ContentType("JWT")).
		WithHeader(jose.HeaderKey("kid"), keyID)
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: algorithm,
		Key:       key,
	}, options)
	require.NoError(t, err)
	signed, err := signer.Sign(payload)
	require.NoError(t, err)
	token, err := signed.CompactSerialize()
	require.NoError(t, err)

	return token
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
