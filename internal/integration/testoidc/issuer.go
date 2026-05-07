//go:build integration

// Package testoidc provides a fake OIDC issuer for integration tests.
package testoidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"
)

// Issuer is a fake OIDC discovery and JWKS endpoint.
type Issuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
	now    time.Time
}

// Start creates and starts a fake OIDC issuer.
func Start(t testing.TB, now time.Time) *Issuer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	issuer := &Issuer{
		key:   key,
		keyID: "test-key",
		now:   now,
	}
	mux := http.NewServeMux()
	issuer.server = httptest.NewServer(mux)
	t.Cleanup(issuer.server.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"issuer":                                issuer.URL(),
			"jwks_uri":                              issuer.URL() + "/jwks",
			"id_token_signing_alg_values_supported": []string{string(jose.RS256)},
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

// URL returns the issuer URL.
func (issuer *Issuer) URL() string {
	return issuer.server.URL
}

// SignToken signs a JWT access token.
func (issuer *Issuer) SignToken(t testing.TB, patchClaims func(map[string]any)) string {
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
		WithHeader(jose.HeaderKey("kid"), issuer.keyID)
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: jose.RS256,
		Key:       issuer.key,
	}, options)
	require.NoError(t, err)
	signed, err := signer.Sign(payload)
	require.NoError(t, err)
	token, err := signed.CompactSerialize()
	require.NoError(t, err)

	return token
}

func writeJSON(t testing.TB, w http.ResponseWriter, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
