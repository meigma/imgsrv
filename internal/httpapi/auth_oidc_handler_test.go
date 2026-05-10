package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	rules map[string]authz.OIDCProvisioningRule
}

func newFakeAuthManagement() *fakeAuthManagement {
	return &fakeAuthManagement{
		rules: map[string]authz.OIDCProvisioningRule{},
	}
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
