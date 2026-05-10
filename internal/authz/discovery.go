package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/meigma/authkit"
	authkitoidc "github.com/meigma/authkit/oidc"
)

const (
	defaultDiscoveryTimeout = 10 * time.Second
	discoveryPath           = "/.well-known/openid-configuration"
	maxDiscoveryBytes       = 1 << 20
)

type discoveryDocument struct {
	Issuer               string   `json:"issuer"`
	JWKSURI              string   `json:"jwks_uri"`
	SigningAlgorithms    []string `json:"id_token_signing_alg_values_supported"`
	TokenEndpointAlgs    []string `json:"token_endpoint_auth_signing_alg_values_supported"`
	UserInfoSigningAlgs  []string `json:"userinfo_signing_alg_values_supported"`
	RequestObjectAlgs    []string `json:"request_object_signing_alg_values_supported"`
	AuthorizationDetails []string `json:"authorization_details_types_supported"`
}

func discoverProvider(
	ctx context.Context,
	client *http.Client,
	issuerURL string,
	audiences []string,
	forwardedClaims []authkit.ClaimPath,
) (authkitoidc.Provider, error) {
	return discoverProviderWithTimeout(
		ctx,
		client,
		issuerURL,
		audiences,
		forwardedClaims,
		defaultDiscoveryTimeout,
	)
}

func discoverProviderWithTimeout(
	ctx context.Context,
	client *http.Client,
	issuerURL string,
	audiences []string,
	forwardedClaims []authkit.ClaimPath,
	timeout time.Duration,
) (authkitoidc.Provider, error) {
	if strings.TrimSpace(issuerURL) == "" {
		return authkitoidc.Provider{}, errors.New("authz: OIDC issuer URL is required")
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	discoveryURL, err := issuerDiscoveryURL(issuerURL)
	if err != nil {
		return authkitoidc.Provider{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return authkitoidc.Provider{}, fmt.Errorf("authz: create OIDC discovery request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return authkitoidc.Provider{}, fmt.Errorf("authz: fetch OIDC discovery document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDiscoveryBytes))
		return authkitoidc.Provider{}, fmt.Errorf(
			"authz: fetch OIDC discovery document: unexpected status %d",
			resp.StatusCode,
		)
	}

	var doc discoveryDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscoveryBytes)).Decode(&doc); err != nil {
		return authkitoidc.Provider{}, fmt.Errorf("authz: decode OIDC discovery document: %w", err)
	}
	if doc.Issuer != issuerURL {
		return authkitoidc.Provider{}, fmt.Errorf(
			"authz: OIDC discovery issuer %q does not match configured issuer %q",
			doc.Issuer,
			issuerURL,
		)
	}

	provider := authkitoidc.Provider{
		Issuer:                     doc.Issuer,
		Audiences:                  cloneStrings(audiences),
		JWKSURL:                    doc.JWKSURI,
		SupportedSigningAlgorithms: cloneStrings(doc.SigningAlgorithms),
		ForwardedClaims:            cloneClaimPaths(forwardedClaims),
	}
	if err := provider.Validate(); err != nil {
		return authkitoidc.Provider{}, err
	}

	return provider, nil
}

func issuerDiscoveryURL(issuerURL string) (string, error) {
	parsed, err := url.Parse(issuerURL)
	if err != nil || !parsed.IsAbs() || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("authz: OIDC issuer URL must be an absolute HTTPS URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + discoveryPath
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String(), nil
}

func mergeProviders(providers []authkitoidc.Provider) []authkitoidc.Provider {
	byIssuer := make(map[string]authkitoidc.Provider, len(providers))
	order := make([]string, 0, len(providers))
	for _, provider := range providers {
		existing, ok := byIssuer[provider.Issuer]
		if !ok {
			byIssuer[provider.Issuer] = provider
			order = append(order, provider.Issuer)
			continue
		}

		existing.Audiences = appendUnique(existing.Audiences, provider.Audiences...)
		existing.ForwardedClaims = appendUniqueClaimPaths(
			existing.ForwardedClaims,
			provider.ForwardedClaims...,
		)
		if len(existing.SupportedSigningAlgorithms) == 0 {
			existing.SupportedSigningAlgorithms = cloneStrings(provider.SupportedSigningAlgorithms)
		}
		byIssuer[provider.Issuer] = existing
	}

	merged := make([]authkitoidc.Provider, 0, len(order))
	for _, issuer := range order {
		merged = append(merged, byIssuer[issuer])
	}

	return merged
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	result := make([]string, 0, len(values)+len(additions))
	for _, value := range append(values, additions...) {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

func appendUniqueClaimPaths(
	paths []authkit.ClaimPath,
	additions ...authkit.ClaimPath,
) []authkit.ClaimPath {
	result := make([]authkit.ClaimPath, 0, len(paths)+len(additions))
	seen := map[string]struct{}{}
	for _, path := range append(paths, additions...) {
		if !path.Valid() {
			continue
		}
		key := strings.Join(path, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, cloneClaimPath(path))
	}

	return result
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)

	return cloned
}

func cloneClaimPaths(paths []authkit.ClaimPath) []authkit.ClaimPath {
	if len(paths) == 0 {
		return nil
	}
	cloned := make([]authkit.ClaimPath, len(paths))
	for i, path := range paths {
		cloned[i] = cloneClaimPath(path)
	}

	return cloned
}

func cloneClaimPath(path authkit.ClaimPath) authkit.ClaimPath {
	if len(path) == 0 {
		return nil
	}
	cloned := make(authkit.ClaimPath, len(path))
	copy(cloned, path)

	return cloned
}
