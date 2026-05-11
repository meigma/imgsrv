package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/authz"
)

func TestOIDCProvisioningRulesRequireAuthManage(t *testing.T) {
	handler := New(Dependencies{
		Auth:           newDenyingAuthService(t),
		AuthManagement: newFakeAuthManagement(),
	})
	req := newAuthManagementRequest(
		http.MethodPost,
		"/v1/auth/oidc-provisioning-rules",
		strings.NewReader(`{"condition":"true"}`),
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assertProblem(t, rec, http.StatusForbidden, "principal is not authorized for action auth.manage")
}

func TestAuthAdminCoreCRUD(t *testing.T) {
	handler := New(Dependencies{
		Auth:           newAcceptingAuthService(t),
		AuthManagement: newFakeAuthManagement(),
	})

	roles := doAuthManagementJSON[roleListResponse](
		t,
		handler,
		http.MethodGet,
		"/v1/auth/roles",
		"",
		http.StatusOK,
	)
	require.Len(t, roles.Roles, 2)
	assert.Equal(t, authz.RoleAuthManager, roles.Roles[1].ID)

	principal := doAuthManagementJSON[principalResponse](
		t,
		handler,
		http.MethodPost,
		"/v1/auth/principals",
		`{"kind":"service","display_name":"publisher","attributes":{"source":"test"}}`,
		http.StatusCreated,
	)
	assert.Equal(t, "principal-1", principal.ID)
	assert.Equal(t, "service", string(principal.Kind))

	req := newAuthManagementRequest(
		http.MethodPut,
		"/v1/auth/principals/principal-1/roles/content-writer",
		nil,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	got := doAuthManagementJSON[principalResponse](
		t,
		handler,
		http.MethodGet,
		"/v1/auth/principals/principal-1",
		"",
		http.StatusOK,
	)
	assert.Equal(t, []string{authz.RoleContentWriter}, got.RoleIDs)

	listed := doAuthManagementJSON[principalListResponse](
		t,
		handler,
		http.MethodGet,
		"/v1/auth/principals",
		"",
		http.StatusOK,
	)
	require.Len(t, listed.Principals, 1)
	assert.Equal(t, "principal-1", listed.Principals[0].ID)

	expiresAt := time.Date(2026, time.May, 11, 12, 0, 0, 0, time.UTC)
	token := doAuthManagementJSON[apiTokenResponse](
		t,
		handler,
		http.MethodPost,
		"/v1/auth/principals/principal-1/api-tokens",
		`{"name":"deploy","expires_at":"`+expiresAt.Format(time.RFC3339)+`"}`,
		http.StatusCreated,
	)
	assert.Equal(t, "token-1", token.ID)
	assert.Equal(t, "principal-1", token.PrincipalID)
	assert.Equal(t, "ak_token-1_secret", token.Plaintext)

	tokens := doAuthManagementJSON[apiTokenListResponse](
		t,
		handler,
		http.MethodGet,
		"/v1/auth/principals/principal-1/api-tokens",
		"",
		http.StatusOK,
	)
	require.Len(t, tokens.APITokens, 1)
	assert.Empty(t, tokens.APITokens[0].Plaintext)

	req = newAuthManagementRequest(
		http.MethodDelete,
		"/v1/auth/api-tokens/token-1",
		nil,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	req = newAuthManagementRequest(
		http.MethodDelete,
		"/v1/auth/principals/principal-1/roles/content-writer",
		nil,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	got = doAuthManagementJSON[principalResponse](
		t,
		handler,
		http.MethodGet,
		"/v1/auth/principals/principal-1",
		"",
		http.StatusOK,
	)
	assert.Empty(t, got.RoleIDs)
}

func TestOIDCProvisioningRuleCRUD(t *testing.T) {
	handler := New(Dependencies{
		Auth:           newAcceptingAuthService(t),
		AuthManagement: newFakeAuthManagement(),
	})

	created := doAuthManagementJSON[oidcProvisioningRuleResponse](
		t,
		handler,
		http.MethodPost,
		"/v1/auth/oidc-provisioning-rules",
		`{
			"id": "github-main-publisher",
			"display_name": "GitHub main publisher",
			"issuer_url": "https://issuer.example",
			"audience": "imgsrv-github",
			"forwarded_claims": ["repository_id", "workflow_ref"],
			"condition": "identity.subject == 'repo:meigma/imgsrv:ref:refs/heads/main'",
			"enabled": true
		}`,
		http.StatusCreated,
	)
	assert.Equal(t, "github-main-publisher", created.ID)
	assert.Equal(t, []string{authz.RoleContentWriter}, created.AssignRoleIDs)

	listed := doAuthManagementJSON[oidcProvisioningRuleListResponse](
		t,
		handler,
		http.MethodGet,
		"/v1/auth/oidc-provisioning-rules",
		"",
		http.StatusOK,
	)
	require.Len(t, listed.Rules, 1)
	assert.Equal(t, created.ID, listed.Rules[0].ID)

	got := doAuthManagementJSON[oidcProvisioningRuleResponse](
		t,
		handler,
		http.MethodGet,
		"/v1/auth/oidc-provisioning-rules/github-main-publisher",
		"",
		http.StatusOK,
	)
	assert.Equal(t, created, got)

	updated := doAuthManagementJSON[oidcProvisioningRuleResponse](
		t,
		handler,
		http.MethodPut,
		"/v1/auth/oidc-provisioning-rules/github-main-publisher",
		`{
			"display_name": "GitHub main publisher disabled",
			"issuer_url": "https://issuer.example",
			"audience": "imgsrv-github",
			"forwarded_claims": ["repository_id"],
			"condition": "claims.repository_id == '123456789'",
			"enabled": false
		}`,
		http.StatusOK,
	)
	assert.Equal(t, "GitHub main publisher disabled", updated.DisplayName)
	assert.False(t, updated.Enabled)

	req := newAuthManagementRequest(
		http.MethodDelete,
		"/v1/auth/oidc-provisioning-rules/github-main-publisher",
		nil,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	req = newAuthManagementRequest(
		http.MethodGet,
		"/v1/auth/oidc-provisioning-rules/github-main-publisher",
		nil,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestOIDCProvisioningRuleRejectsBadCEL(t *testing.T) {
	handler := New(Dependencies{
		Auth:           newAcceptingAuthService(t),
		AuthManagement: newFakeAuthManagement(),
	})
	req := newAuthManagementRequest(
		http.MethodPost,
		"/v1/auth/oidc-provisioning-rules",
		strings.NewReader(`{
			"id": "bad-rule",
			"display_name": "Bad rule",
			"issuer_url": "https://issuer.example",
			"audience": "imgsrv-github",
			"condition": "claims.repository_id"
		}`),
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assertProblem(t, rec, http.StatusBadRequest, "condition must produce bool")
}

func TestAuthManagementErrorStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{
			name:   "provisioning rule not found",
			err:    authkit.ErrProvisioningRuleNotFound,
			status: http.StatusNotFound,
		},
		{
			name:   "principal not found",
			err:    authkit.ErrPrincipalNotFound,
			status: http.StatusNotFound,
		},
		{
			name:   "validation",
			err:    errors.New("authz: OIDC audience is required"),
			status: http.StatusBadRequest,
		},
		{
			name:   "server failure",
			err:    errors.New("postgres: query authkit_principals: connection refused"),
			status: http.StatusInternalServerError,
		},
		{
			name:   "provider backend failure",
			err:    errors.New("authz: fetch OIDC discovery document: unexpected status 503"),
			status: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.status, authManagementErrorStatus(tt.err))
		})
	}
}

func newAuthManagementRequest(method string, target string, body *strings.Reader) *http.Request {
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		reader = body
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Authorization", "Bearer "+testBearerToken)
	if method == http.MethodPost || method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}

	return req
}

func doAuthManagementJSON[T any](
	t *testing.T,
	handler http.Handler,
	method string,
	target string,
	body string,
	wantStatus int,
) T {
	t.Helper()

	req := newAuthManagementRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, wantStatus, rec.Code)

	var got T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	return got
}

type fakeAuthManagement struct {
	rules      map[string]authz.OIDCProvisioningRule
	principals map[string]authz.Principal
	tokens     map[string]authz.APITokenMetadata
}

func newFakeAuthManagement() *fakeAuthManagement {
	return &fakeAuthManagement{
		rules:      map[string]authz.OIDCProvisioningRule{},
		principals: map[string]authz.Principal{},
		tokens:     map[string]authz.APITokenMetadata{},
	}
}

func (f *fakeAuthManagement) ListRoles(context.Context) ([]authz.Role, error) {
	return []authz.Role{
		{
			ID:          authz.RoleContentWriter,
			DisplayName: "Content writer",
			Description: "Can write imgsrv content.",
			Actions:     []string{authz.ActionContentWrite},
		},
		{
			ID:          authz.RoleAuthManager,
			DisplayName: "Auth manager",
			Description: "Can manage imgsrv authentication policy.",
			Actions:     []string{authz.ActionAuthManage},
		},
	}, nil
}

func (f *fakeAuthManagement) CreatePrincipal(
	_ context.Context,
	req authz.CreatePrincipalRequest,
) (authz.Principal, error) {
	principal := authz.Principal{
		ID:          "principal-1",
		Kind:        req.Kind,
		DisplayName: req.DisplayName,
		Attributes:  req.Attributes,
	}
	f.principals[principal.ID] = principal

	return principal, nil
}

func (f *fakeAuthManagement) ListPrincipals(context.Context) ([]authz.Principal, error) {
	principals := make([]authz.Principal, 0, len(f.principals))
	for _, principal := range f.principals {
		principals = append(principals, principal)
	}
	sort.Slice(principals, func(i, j int) bool {
		return principals[i].ID < principals[j].ID
	})

	return principals, nil
}

func (f *fakeAuthManagement) FindPrincipal(_ context.Context, id string) (authz.Principal, error) {
	principal, ok := f.principals[id]
	if !ok {
		return authz.Principal{}, authkit.ErrPrincipalNotFound
	}

	return principal, nil
}

func (f *fakeAuthManagement) AssignPrincipalRole(_ context.Context, principalID string, roleID string) error {
	principal, ok := f.principals[principalID]
	if !ok {
		return authkit.ErrPrincipalNotFound
	}
	if slices.Contains(principal.RoleIDs, roleID) {
		f.principals[principalID] = principal

		return nil
	}
	principal.RoleIDs = append(principal.RoleIDs, roleID)
	sort.Strings(principal.RoleIDs)
	f.principals[principalID] = principal

	return nil
}

func (f *fakeAuthManagement) UnassignPrincipalRole(_ context.Context, principalID string, roleID string) error {
	principal, ok := f.principals[principalID]
	if !ok {
		return authkit.ErrPrincipalNotFound
	}
	filtered := principal.RoleIDs[:0]
	for _, assigned := range principal.RoleIDs {
		if assigned != roleID {
			filtered = append(filtered, assigned)
		}
	}
	principal.RoleIDs = filtered
	f.principals[principalID] = principal

	return nil
}

func (f *fakeAuthManagement) IssueAPIToken(
	_ context.Context,
	req authz.IssueAPITokenRequest,
) (authz.IssuedAPIToken, error) {
	if _, ok := f.principals[req.PrincipalID]; !ok {
		return authz.IssuedAPIToken{}, authkit.ErrPrincipalNotFound
	}
	token := authz.APITokenMetadata{
		ID:          "token-1",
		PrincipalID: req.PrincipalID,
		Name:        req.Name,
		ExpiresAt:   req.ExpiresAt,
	}
	f.tokens[token.ID] = token

	return authz.IssuedAPIToken{
		APITokenMetadata: token,
		Plaintext:        "ak_token-1_secret",
	}, nil
}

func (f *fakeAuthManagement) ListPrincipalAPITokens(
	_ context.Context,
	principalID string,
) ([]authz.APITokenMetadata, error) {
	if _, ok := f.principals[principalID]; !ok {
		return nil, authkit.ErrPrincipalNotFound
	}
	tokens := make([]authz.APITokenMetadata, 0)
	for _, token := range f.tokens {
		if token.PrincipalID == principalID {
			tokens = append(tokens, token)
		}
	}
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].ID < tokens[j].ID
	})

	return tokens, nil
}

func (f *fakeAuthManagement) RevokeAPIToken(_ context.Context, tokenID string) error {
	if _, ok := f.tokens[tokenID]; !ok {
		return authkit.ErrPrincipalNotFound
	}
	delete(f.tokens, tokenID)

	return nil
}

func (f *fakeAuthManagement) CreateOIDCProvisioningRule(
	_ context.Context,
	req authz.SaveOIDCProvisioningRuleRequest,
) (authz.OIDCProvisioningRule, error) {
	if err := provisioning.ValidateCondition(req.Condition); err != nil {
		return authz.OIDCProvisioningRule{}, err
	}
	if req.ID == "" {
		req.ID = "generated-rule"
	}
	rule := fakeRule(req)
	f.rules[rule.ID] = rule

	return rule, nil
}

func (f *fakeAuthManagement) UpdateOIDCProvisioningRule(
	_ context.Context,
	req authz.SaveOIDCProvisioningRuleRequest,
) (authz.OIDCProvisioningRule, error) {
	if _, ok := f.rules[req.ID]; !ok {
		return authz.OIDCProvisioningRule{}, authkit.ErrProvisioningRuleNotFound
	}
	if err := provisioning.ValidateCondition(req.Condition); err != nil {
		return authz.OIDCProvisioningRule{}, err
	}
	rule := fakeRule(req)
	f.rules[rule.ID] = rule

	return rule, nil
}

func (f *fakeAuthManagement) DeleteOIDCProvisioningRule(_ context.Context, id string) error {
	if _, ok := f.rules[id]; !ok {
		return authkit.ErrProvisioningRuleNotFound
	}
	delete(f.rules, id)

	return nil
}

func (f *fakeAuthManagement) FindOIDCProvisioningRule(
	_ context.Context,
	id string,
) (authz.OIDCProvisioningRule, error) {
	rule, ok := f.rules[id]
	if !ok {
		return authz.OIDCProvisioningRule{}, authkit.ErrProvisioningRuleNotFound
	}

	return rule, nil
}

func (f *fakeAuthManagement) ListOIDCProvisioningRules(
	context.Context,
) ([]authz.OIDCProvisioningRule, error) {
	rules := make([]authz.OIDCProvisioningRule, 0, len(f.rules))
	for _, rule := range f.rules {
		rules = append(rules, rule)
	}

	return rules, nil
}

func fakeRule(req authz.SaveOIDCProvisioningRuleRequest) authz.OIDCProvisioningRule {
	return authz.OIDCProvisioningRule{
		ID:              req.ID,
		DisplayName:     req.DisplayName,
		IssuerURL:       req.IssuerURL,
		Audience:        req.Audience,
		ForwardedClaims: req.ForwardedClaims,
		Condition:       req.Condition,
		AssignRoleIDs:   []string{authz.RoleContentWriter},
		Enabled:         req.Enabled,
	}
}
