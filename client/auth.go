package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// AuthClient provides auth-management API operations.
type AuthClient interface {
	// ListRoles returns imgsrv's built-in auth roles.
	ListRoles(context.Context) ([]Role, error)

	// CreatePrincipal creates a principal.
	CreatePrincipal(context.Context, CreatePrincipalRequest) (Principal, error)

	// ListPrincipals returns principals.
	ListPrincipals(context.Context) ([]Principal, error)

	// GetPrincipal returns one principal.
	GetPrincipal(context.Context, string) (Principal, error)

	// AssignPrincipalRole assigns a role to a principal.
	AssignPrincipalRole(context.Context, string, string) error

	// UnassignPrincipalRole removes a role from a principal.
	UnassignPrincipalRole(context.Context, string, string) error

	// IssueAPIToken issues an API token for a principal.
	IssueAPIToken(context.Context, string, IssueAPITokenRequest) (APIToken, error)

	// ListPrincipalAPITokens returns API-token metadata for a principal.
	ListPrincipalAPITokens(context.Context, string) ([]APIToken, error)

	// RevokeAPIToken revokes an API token.
	RevokeAPIToken(context.Context, string) error

	// CreateOIDCProvisioningRule creates an OIDC provisioning rule.
	CreateOIDCProvisioningRule(context.Context, SaveOIDCProvisioningRuleRequest) (OIDCProvisioningRule, error)

	// ListOIDCProvisioningRules returns configured OIDC provisioning rules.
	ListOIDCProvisioningRules(context.Context) ([]OIDCProvisioningRule, error)

	// GetOIDCProvisioningRule returns one OIDC provisioning rule.
	GetOIDCProvisioningRule(context.Context, string) (OIDCProvisioningRule, error)

	// UpdateOIDCProvisioningRule replaces one OIDC provisioning rule.
	UpdateOIDCProvisioningRule(
		context.Context,
		string,
		SaveOIDCProvisioningRuleRequest,
	) (OIDCProvisioningRule, error)

	// DeleteOIDCProvisioningRule deletes one OIDC provisioning rule.
	DeleteOIDCProvisioningRule(context.Context, string) error

	// PreviewOIDCProvisioningRuleReconciliation previews rule-granted role cleanup.
	PreviewOIDCProvisioningRuleReconciliation(
		context.Context,
		string,
	) (OIDCProvisioningRuleReconciliation, error)

	// ReconcileOIDCProvisioningRule removes rule-granted roles from existing principals.
	ReconcileOIDCProvisioningRule(
		context.Context,
		string,
	) (OIDCProvisioningRuleReconciliation, error)
}

// HTTPAuthClient is the concrete HTTP implementation of AuthClient.
type HTTPAuthClient struct {
	// transport carries the HTTP configuration shared with the parent Client.
	transport *transport
}

var _ AuthClient = (*HTTPAuthClient)(nil)

// Role describes an imgsrv built-in role.
type Role struct {
	// ID is the stable role identifier.
	ID string `json:"id"`

	// DisplayName is the role label.
	DisplayName string `json:"display_name"`

	// Description explains the role's intended use.
	Description string `json:"description"`

	// Actions are the authorization actions granted by the role.
	Actions []string `json:"actions"`
}

// Principal describes an auth principal.
type Principal struct {
	// ID is the stable principal identifier.
	ID string `json:"id"`

	// Kind classifies the principal.
	Kind string `json:"kind"`

	// DisplayName is the principal label.
	DisplayName string `json:"display_name"`

	// Attributes contains application-owned principal metadata.
	Attributes map[string]any `json:"attributes,omitempty"`

	// RoleIDs are the local roles currently assigned to the principal.
	RoleIDs []string `json:"role_ids"`
}

// CreatePrincipalRequest creates a principal.
type CreatePrincipalRequest struct {
	// Kind classifies the principal.
	Kind string `json:"kind"`

	// DisplayName is the principal label.
	DisplayName string `json:"display_name"`

	// Attributes contains application-owned principal metadata.
	Attributes map[string]any `json:"attributes,omitempty"`
}

// IssueAPITokenRequest issues an API token for a principal.
type IssueAPITokenRequest struct {
	// Name is an optional token label.
	Name string `json:"name"`

	// ExpiresAt is the time after which the token no longer authenticates.
	ExpiresAt time.Time `json:"expires_at"`
}

// APIToken describes API-token metadata. Plaintext is present only in issue responses.
type APIToken struct {
	// ID is the stable token identifier.
	ID string `json:"id"`

	// PrincipalID identifies the principal the token authenticates as.
	PrincipalID string `json:"principal_id"`

	// Name is an optional token label.
	Name string `json:"name"`

	// ExpiresAt is the time after which the token no longer authenticates.
	ExpiresAt time.Time `json:"expires_at"`

	// LastUsedAt records the most recent successful use when known.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`

	// RevokedAt records when the token was revoked.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`

	// Plaintext is the full bearer token secret shown once after issue.
	Plaintext string `json:"plaintext,omitempty"`
}

// SaveOIDCProvisioningRuleRequest creates or updates an OIDC provisioning rule.
type SaveOIDCProvisioningRuleRequest struct {
	// ID is the stable rule identifier. Empty IDs are generated by the server on create.
	ID string `json:"id,omitempty"`

	// DisplayName is the operator-facing rule label.
	DisplayName string `json:"display_name"`

	// IssuerURL is the trusted OIDC issuer URL.
	IssuerURL string `json:"issuer_url"`

	// Audience is the accepted JWT audience.
	Audience string `json:"audience"`

	// ForwardedClaims are top-level verified JWT claims exposed to the CEL condition.
	ForwardedClaims []string `json:"forwarded_claims,omitempty"`

	// Condition is the CEL bool expression evaluated during provisioning.
	Condition string `json:"condition"`

	// Enabled controls whether the rule participates in provisioning.
	Enabled *bool `json:"enabled,omitempty"`
}

// OIDCProvisioningRule describes an operator-managed OIDC provisioning rule.
type OIDCProvisioningRule struct {
	// ID is the stable rule identifier.
	ID string `json:"id"`

	// DisplayName is the operator-facing rule label.
	DisplayName string `json:"display_name"`

	// IssuerURL is the trusted OIDC issuer URL.
	IssuerURL string `json:"issuer_url"`

	// Audience is the accepted JWT audience.
	Audience string `json:"audience"`

	// ForwardedClaims are top-level verified JWT claims exposed to the CEL condition.
	ForwardedClaims []string `json:"forwarded_claims,omitempty"`

	// Condition is the CEL bool expression evaluated during provisioning.
	Condition string `json:"condition"`

	// AssignRoleIDs are local role IDs assigned when Condition matches.
	AssignRoleIDs []string `json:"assign_role_ids"`

	// Enabled controls whether the rule participates in provisioning.
	Enabled bool `json:"enabled"`
}

// OIDCProvisioningRuleReconciliation describes principals affected by rule cleanup.
type OIDCProvisioningRuleReconciliation struct {
	// RuleID is the provisioning rule being reconciled.
	RuleID string `json:"rule_id"`

	// UnassignRoleIDs are the roles previewed or removed by reconciliation.
	UnassignRoleIDs []string `json:"unassign_role_ids"`

	// Principals are the principals that would be or were reconciled.
	Principals []Principal `json:"principals"`

	// Applied is true when reconciliation was applied, and false for preview responses.
	Applied bool `json:"applied"`
}

type oidcProvisioningRuleList struct {
	Rules []OIDCProvisioningRule `json:"rules"`
}

type roleList struct {
	Roles []Role `json:"roles"`
}

type principalList struct {
	Principals []Principal `json:"principals"`
}

type apiTokenList struct {
	APITokens []APIToken `json:"api_tokens"`
}

// newHTTPAuthClient binds an auth-management operation group to the shared transport.
func newHTTPAuthClient(transport *transport) *HTTPAuthClient {
	return &HTTPAuthClient{transport: transport}
}

// ListRoles returns imgsrv's built-in auth roles.
func (client *HTTPAuthClient) ListRoles(ctx context.Context) ([]Role, error) {
	var list roleList
	err := client.transport.do(ctx, http.MethodGet, "/v1/auth/roles", nil, 0, nil, &list)

	return list.Roles, err
}

// CreatePrincipal creates a principal.
func (client *HTTPAuthClient) CreatePrincipal(
	ctx context.Context,
	request CreatePrincipalRequest,
) (Principal, error) {
	var principal Principal
	err := client.transport.doJSON(ctx, "/v1/auth/principals", request, http.StatusCreated, &principal)

	return principal, err
}

// ListPrincipals returns principals.
func (client *HTTPAuthClient) ListPrincipals(ctx context.Context) ([]Principal, error) {
	var list principalList
	err := client.transport.do(ctx, http.MethodGet, "/v1/auth/principals", nil, 0, nil, &list)

	return list.Principals, err
}

// GetPrincipal returns one principal.
func (client *HTTPAuthClient) GetPrincipal(ctx context.Context, principalID string) (Principal, error) {
	var principal Principal
	err := client.transport.do(
		ctx,
		http.MethodGet,
		"/v1/auth/principals/"+url.PathEscape(principalID),
		nil,
		0,
		nil,
		&principal,
	)

	return principal, err
}

// AssignPrincipalRole assigns a role to a principal.
func (client *HTTPAuthClient) AssignPrincipalRole(ctx context.Context, principalID string, roleID string) error {
	resp, err := client.transport.doResponse(
		ctx,
		http.MethodPut,
		"/v1/auth/principals/"+url.PathEscape(principalID)+"/roles/"+url.PathEscape(roleID),
		nil,
		0,
		nil,
		http.StatusNoContent,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return nil
}

// UnassignPrincipalRole removes a role from a principal.
func (client *HTTPAuthClient) UnassignPrincipalRole(ctx context.Context, principalID string, roleID string) error {
	resp, err := client.transport.doResponse(
		ctx,
		http.MethodDelete,
		"/v1/auth/principals/"+url.PathEscape(principalID)+"/roles/"+url.PathEscape(roleID),
		nil,
		0,
		nil,
		http.StatusNoContent,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return nil
}

// IssueAPIToken issues an API token for a principal.
func (client *HTTPAuthClient) IssueAPIToken(
	ctx context.Context,
	principalID string,
	request IssueAPITokenRequest,
) (APIToken, error) {
	var token APIToken
	err := client.transport.doJSON(
		ctx,
		"/v1/auth/principals/"+url.PathEscape(principalID)+"/api-tokens",
		request,
		http.StatusCreated,
		&token,
	)

	return token, err
}

// ListPrincipalAPITokens returns API-token metadata for a principal.
func (client *HTTPAuthClient) ListPrincipalAPITokens(ctx context.Context, principalID string) ([]APIToken, error) {
	var list apiTokenList
	err := client.transport.do(
		ctx,
		http.MethodGet,
		"/v1/auth/principals/"+url.PathEscape(principalID)+"/api-tokens",
		nil,
		0,
		nil,
		&list,
	)

	return list.APITokens, err
}

// RevokeAPIToken revokes an API token.
func (client *HTTPAuthClient) RevokeAPIToken(ctx context.Context, tokenID string) error {
	resp, err := client.transport.doResponse(
		ctx,
		http.MethodDelete,
		"/v1/auth/api-tokens/"+url.PathEscape(tokenID),
		nil,
		0,
		nil,
		http.StatusNoContent,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return nil
}

// CreateOIDCProvisioningRule creates an OIDC provisioning rule.
func (client *HTTPAuthClient) CreateOIDCProvisioningRule(
	ctx context.Context,
	request SaveOIDCProvisioningRuleRequest,
) (OIDCProvisioningRule, error) {
	var rule OIDCProvisioningRule
	err := client.transport.doJSON(
		ctx,
		"/v1/auth/oidc-provisioning-rules",
		request,
		http.StatusCreated,
		&rule,
	)

	return rule, err
}

// ListOIDCProvisioningRules returns configured OIDC provisioning rules.
func (client *HTTPAuthClient) ListOIDCProvisioningRules(
	ctx context.Context,
) ([]OIDCProvisioningRule, error) {
	var list oidcProvisioningRuleList
	err := client.transport.do(
		ctx,
		http.MethodGet,
		"/v1/auth/oidc-provisioning-rules",
		nil,
		0,
		nil,
		&list,
	)

	return list.Rules, err
}

// GetOIDCProvisioningRule returns one OIDC provisioning rule.
func (client *HTTPAuthClient) GetOIDCProvisioningRule(
	ctx context.Context,
	ruleID string,
) (OIDCProvisioningRule, error) {
	var rule OIDCProvisioningRule
	err := client.transport.do(
		ctx,
		http.MethodGet,
		"/v1/auth/oidc-provisioning-rules/"+url.PathEscape(ruleID),
		nil,
		0,
		nil,
		&rule,
	)

	return rule, err
}

// UpdateOIDCProvisioningRule replaces one OIDC provisioning rule.
func (client *HTTPAuthClient) UpdateOIDCProvisioningRule(
	ctx context.Context,
	ruleID string,
	request SaveOIDCProvisioningRuleRequest,
) (OIDCProvisioningRule, error) {
	var rule OIDCProvisioningRule
	err := client.transport.doJSONMethod(
		ctx,
		http.MethodPut,
		"/v1/auth/oidc-provisioning-rules/"+url.PathEscape(ruleID),
		request,
		http.StatusOK,
		&rule,
	)

	return rule, err
}

// DeleteOIDCProvisioningRule deletes one OIDC provisioning rule.
func (client *HTTPAuthClient) DeleteOIDCProvisioningRule(ctx context.Context, ruleID string) error {
	resp, err := client.transport.doResponse(
		ctx,
		http.MethodDelete,
		"/v1/auth/oidc-provisioning-rules/"+url.PathEscape(ruleID),
		nil,
		0,
		nil,
		http.StatusNoContent,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return nil
}

// PreviewOIDCProvisioningRuleReconciliation previews rule-granted role cleanup.
func (client *HTTPAuthClient) PreviewOIDCProvisioningRuleReconciliation(
	ctx context.Context,
	ruleID string,
) (OIDCProvisioningRuleReconciliation, error) {
	var reconciliation OIDCProvisioningRuleReconciliation
	err := client.transport.do(
		ctx,
		http.MethodGet,
		"/v1/auth/oidc-provisioning-rules/"+url.PathEscape(ruleID)+"/reconciliation",
		nil,
		0,
		nil,
		&reconciliation,
	)

	return reconciliation, err
}

// ReconcileOIDCProvisioningRule removes rule-granted roles from existing principals.
func (client *HTTPAuthClient) ReconcileOIDCProvisioningRule(
	ctx context.Context,
	ruleID string,
) (OIDCProvisioningRuleReconciliation, error) {
	var reconciliation OIDCProvisioningRuleReconciliation
	err := client.transport.do(
		ctx,
		http.MethodPost,
		"/v1/auth/oidc-provisioning-rules/"+url.PathEscape(ruleID)+"/reconciliation",
		nil,
		0,
		nil,
		&reconciliation,
	)

	return reconciliation, err
}
