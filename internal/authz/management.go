package authz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	authkitmanagement "github.com/meigma/authkit/management"
	authkitoidc "github.com/meigma/authkit/oidc"
	"github.com/meigma/authkit/provisioning"

	safelog "github.com/meigma/imgsrv/internal/logging"
)

const (
	managedAudienceClaim           = "aud"
	managedAudienceConditionPrefix = "hasAny(claims.aud, ["
	managedAudienceConditionMiddle = "]) && ("
	managedAudienceConditionSuffix = ")"
)

// ManagementStore is the authkit store surface used by imgsrv auth-management APIs.
type ManagementStore interface {
	RoleStore
	BootstrapStore
	authkit.PrincipalFinder
	authkit.PrincipalRoleUnassigner
	apikey.TokenMetadataLister
	authkitoidc.ProviderTrustStore
	authkit.ProvisioningRuleCreator
	authkit.ProvisioningRuleUpdater
	authkit.ProvisioningRuleDeleter
	authkit.ProvisioningRuleFinder
	authkit.ProvisioningRuleLister
}

// ManagementService manages imgsrv-owned authkit OIDC provisioning configuration.
type ManagementService struct {
	store      ManagementStore
	httpClient *http.Client
	logger     *slog.Logger
}

// ManagementConfig configures a ManagementService.
type ManagementConfig struct {
	// Store persists trusted providers, provisioning rules, and built-in roles.
	Store ManagementStore

	// HTTPClient fetches OIDC discovery documents and JWKS keys. Nil uses a bounded default client.
	HTTPClient *http.Client

	// Logger receives sanitized auth-management logs. Nil selects a discarded logger.
	Logger *slog.Logger
}

// OIDCProvisioningRule is the operator-facing view of an authkit provisioning rule and provider trust.
type OIDCProvisioningRule struct {
	// ID is the stable rule identifier.
	ID string

	// DisplayName is the operator-facing rule label.
	DisplayName string

	// IssuerURL is the trusted OIDC issuer URL.
	IssuerURL string

	// Audience is the accepted JWT audience configured for this rule's provider.
	Audience string

	// ForwardedClaims are top-level verified JWT claims exposed to the CEL condition.
	ForwardedClaims []string

	// Condition is the CEL bool expression evaluated during provisioning.
	Condition string

	// AssignRoleIDs are local role IDs assigned when Condition matches.
	AssignRoleIDs []string

	// Enabled controls whether the rule participates in provisioning.
	Enabled bool
}

// OIDCProvisioningRuleReconciliation describes principals affected by a rule cleanup.
type OIDCProvisioningRuleReconciliation struct {
	// RuleID is the provisioning rule being reconciled.
	RuleID string

	// UnassignRoleIDs are the roles previewed or removed by reconciliation.
	UnassignRoleIDs []string

	// Principals are the principals that would be or were reconciled.
	Principals []Principal

	// Applied is true when reconciliation was applied, and false for preview responses.
	Applied bool
}

// SaveOIDCProvisioningRuleRequest creates or updates an OIDC provisioning rule.
type SaveOIDCProvisioningRuleRequest struct {
	// ID is the stable rule identifier. Empty IDs are generated on create.
	ID string

	// DisplayName is the operator-facing rule label.
	DisplayName string

	// IssuerURL is the trusted OIDC issuer URL.
	IssuerURL string

	// Audience is the accepted JWT audience.
	Audience string

	// ForwardedClaims are top-level verified JWT claims exposed to the CEL condition.
	ForwardedClaims []string

	// Condition is the CEL bool expression evaluated during provisioning.
	Condition string

	// Enabled controls whether the rule participates in provisioning.
	Enabled bool
}

// Role is the operator-facing view of an imgsrv built-in role.
type Role struct {
	// ID is the stable role identifier.
	ID string

	// DisplayName is the operator-facing role label.
	DisplayName string

	// Description explains the role's intended use.
	Description string

	// Actions are the authorization actions granted by the role.
	Actions []string
}

// Principal is the operator-facing view of an authkit principal.
type Principal struct {
	// ID is the stable principal identifier.
	ID string

	// Kind classifies the principal.
	Kind authkit.PrincipalKind

	// DisplayName is the operator-facing principal label.
	DisplayName string

	// Attributes contains application-owned principal metadata.
	Attributes map[string]any

	// RoleIDs are the local roles currently assigned to the principal.
	RoleIDs []string
}

// CreatePrincipalRequest describes an operator-created principal.
type CreatePrincipalRequest struct {
	// Kind classifies the principal.
	Kind authkit.PrincipalKind

	// DisplayName is the operator-facing principal label.
	DisplayName string

	// Attributes contains application-owned principal metadata.
	Attributes map[string]any
}

// APITokenMetadata is the operator-facing token view without secret material.
type APITokenMetadata struct {
	// ID is the stable token identifier.
	ID string

	// PrincipalID identifies the principal the token authenticates as.
	PrincipalID string

	// Name is an optional operator-facing token label.
	Name string

	// ExpiresAt is the time after which the token no longer authenticates.
	ExpiresAt time.Time

	// LastUsedAt records the most recent successful use when known.
	LastUsedAt *time.Time

	// RevokedAt records when the token was revoked.
	RevokedAt *time.Time
}

// IssueAPITokenRequest describes a request to issue an API token for a principal.
type IssueAPITokenRequest struct {
	// PrincipalID identifies the principal the token authenticates as.
	PrincipalID string

	// Name is an optional operator-facing token label.
	Name string

	// ExpiresAt is the time after which the token no longer authenticates.
	ExpiresAt time.Time
}

// IssuedAPIToken is an issued token response with plaintext shown once.
type IssuedAPIToken struct {
	// APITokenMetadata contains the persisted token metadata.
	APITokenMetadata

	// Plaintext is the full bearer token secret shown once.
	Plaintext string
}

// NewManagementService constructs an auth-management service.
func NewManagementService(config ManagementConfig) *ManagementService {
	logger := config.Logger
	if logger == nil {
		logger = safelog.Nop()
	}

	return &ManagementService{
		store:      config.Store,
		httpClient: config.HTTPClient,
		logger:     logger,
	}
}

// ListRoles returns imgsrv's built-in roles.
func (s *ManagementService) ListRoles(context.Context) ([]Role, error) {
	return builtinRoleDefinitions(), nil
}

// CreatePrincipal creates an internal principal.
func (s *ManagementService) CreatePrincipal(
	ctx context.Context,
	req CreatePrincipalRequest,
) (Principal, error) {
	if s == nil || s.store == nil {
		return Principal{}, errManagementStoreRequired()
	}
	principal, err := s.store.CreatePrincipal(ctx, authkit.CreatePrincipalRequest{
		Kind:        req.Kind,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Attributes:  req.Attributes,
	})
	if err != nil {
		return Principal{}, err
	}

	result, err := s.principalWithRoles(ctx, principal)
	if err != nil {
		return Principal{}, err
	}
	s.logger.InfoContext(
		ctx,
		"principal created",
		"operation",
		"auth.create_principal",
		"principal_id",
		result.ID,
		"principal_kind",
		string(result.Kind),
		"role_count",
		len(result.RoleIDs),
	)

	return result, nil
}

// ListPrincipals returns principals with their assigned role IDs.
func (s *ManagementService) ListPrincipals(ctx context.Context) ([]Principal, error) {
	if s == nil || s.store == nil {
		return nil, errManagementStoreRequired()
	}
	principals, err := s.store.ListPrincipals(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]Principal, 0, len(principals))
	for _, principal := range principals {
		item, err := s.principalWithRoles(ctx, principal)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	return result, nil
}

// FindPrincipal returns one principal with its assigned role IDs.
func (s *ManagementService) FindPrincipal(ctx context.Context, id string) (Principal, error) {
	if s == nil || s.store == nil {
		return Principal{}, errManagementStoreRequired()
	}
	principal, err := s.store.FindPrincipal(ctx, id)
	if err != nil {
		return Principal{}, err
	}

	return s.principalWithRoles(ctx, principal)
}

// AssignPrincipalRole assigns a built-in role to a principal.
func (s *ManagementService) AssignPrincipalRole(ctx context.Context, principalID string, roleID string) error {
	if s == nil || s.store == nil {
		return errManagementStoreRequired()
	}
	if err := validateBuiltinRoleID(roleID); err != nil {
		return err
	}
	if err := EnsureBuiltinRoles(ctx, s.store); err != nil {
		return err
	}

	if err := s.store.AssignPrincipalRole(ctx, authkit.AssignPrincipalRoleRequest{
		PrincipalID: principalID,
		RoleID:      roleID,
	}); err != nil {
		return err
	}
	s.logger.InfoContext(
		ctx,
		"principal role assigned",
		"operation",
		"auth.assign_role",
		"principal_id",
		principalID,
		"role_id",
		roleID,
	)

	return nil
}

// UnassignPrincipalRole removes a built-in role from a principal.
func (s *ManagementService) UnassignPrincipalRole(ctx context.Context, principalID string, roleID string) error {
	if s == nil || s.store == nil {
		return errManagementStoreRequired()
	}
	if err := validateBuiltinRoleID(roleID); err != nil {
		return err
	}

	if err := s.store.UnassignPrincipalRole(ctx, authkit.UnassignPrincipalRoleRequest{
		PrincipalID: principalID,
		RoleID:      roleID,
	}); err != nil {
		return err
	}
	s.logger.InfoContext(
		ctx,
		"principal role unassigned",
		"operation",
		"auth.unassign_role",
		"principal_id",
		principalID,
		"role_id",
		roleID,
	)

	return nil
}

// IssueAPIToken issues and links an API token for a principal.
func (s *ManagementService) IssueAPIToken(ctx context.Context, req IssueAPITokenRequest) (IssuedAPIToken, error) {
	if s == nil || s.store == nil {
		return IssuedAPIToken{}, errManagementStoreRequired()
	}
	if _, err := s.store.FindPrincipal(ctx, req.PrincipalID); err != nil {
		return IssuedAPIToken{}, err
	}
	service, err := s.authkitManagementService()
	if err != nil {
		return IssuedAPIToken{}, err
	}
	issued, err := service.IssueAPIToken(ctx, authkitmanagement.IssueAPITokenRequest{
		PrincipalID: req.PrincipalID,
		Name:        strings.TrimSpace(req.Name),
		ExpiresAt:   req.ExpiresAt,
	})
	if err != nil {
		return IssuedAPIToken{}, err
	}

	result := IssuedAPIToken{
		APITokenMetadata: APITokenMetadata{
			ID:          issued.ID,
			PrincipalID: issued.Identity.PrincipalID,
			Name:        strings.TrimSpace(req.Name),
			ExpiresAt:   issued.ExpiresAt,
		},
		Plaintext: issued.Plaintext,
	}
	s.logger.InfoContext(
		ctx,
		"api token issued",
		"operation",
		"auth.issue_api_token",
		"token_id",
		result.ID,
		"principal_id",
		result.PrincipalID,
		"expires_at",
		result.ExpiresAt,
	)

	return result, nil
}

// ListPrincipalAPITokens returns token metadata for a principal.
func (s *ManagementService) ListPrincipalAPITokens(
	ctx context.Context,
	principalID string,
) ([]APITokenMetadata, error) {
	if s == nil || s.store == nil {
		return nil, errManagementStoreRequired()
	}
	if _, err := s.store.FindPrincipal(ctx, principalID); err != nil {
		return nil, err
	}
	tokens, err := s.store.ListPrincipalTokenMetadata(ctx, principalID)
	if err != nil {
		return nil, err
	}

	result := make([]APITokenMetadata, 0, len(tokens))
	for _, token := range tokens {
		result = append(result, newAPITokenMetadata(token))
	}

	return result, nil
}

// RevokeAPIToken revokes an API token.
func (s *ManagementService) RevokeAPIToken(ctx context.Context, tokenID string) error {
	if s == nil || s.store == nil {
		return errManagementStoreRequired()
	}
	service, err := s.authkitManagementService()
	if err != nil {
		return err
	}

	if err := service.RevokeAPIToken(ctx, tokenID); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "api token revoked", "operation", "auth.revoke_api_token", "token_id", tokenID)

	return nil
}

// CreateOIDCProvisioningRule creates a trusted provider and provisioning rule.
func (s *ManagementService) CreateOIDCProvisioningRule(
	ctx context.Context,
	req SaveOIDCProvisioningRuleRequest,
) (OIDCProvisioningRule, error) {
	if s == nil || s.store == nil {
		return OIDCProvisioningRule{}, errManagementStoreRequired()
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	condition, err := managedRuleCondition(req.Audience, req.Condition)
	if err != nil {
		return OIDCProvisioningRule{}, err
	}

	provider, err := s.trustProvider(ctx, req)
	if err != nil {
		return OIDCProvisioningRule{}, err
	}
	if err = EnsureBuiltinRoles(ctx, s.store); err != nil {
		return OIDCProvisioningRule{}, err
	}

	rule, err := s.store.CreateProvisioningRule(ctx, authkit.CreateProvisioningRuleRequest{
		ID:            strings.TrimSpace(req.ID),
		DisplayName:   strings.TrimSpace(req.DisplayName),
		Provider:      provider.Issuer,
		Condition:     condition,
		AssignRoleIDs: []string{RoleContentWriter},
		Enabled:       req.Enabled,
	})
	if err != nil {
		return OIDCProvisioningRule{}, err
	}

	result := newOIDCProvisioningRule(rule, provider)
	s.logger.InfoContext(
		ctx,
		"oidc provisioning rule created",
		"operation",
		"auth.create_oidc_provisioning_rule",
		"rule_id",
		result.ID,
		"issuer_url",
		result.IssuerURL,
		"enabled",
		result.Enabled,
		"forwarded_claim_count",
		len(result.ForwardedClaims),
	)

	return result, nil
}

func (s *ManagementService) principalWithRoles(
	ctx context.Context,
	principal authkit.Principal,
) (Principal, error) {
	assignments, err := s.store.ListPrincipalRoleAssignments(ctx, principal.ID)
	if err != nil {
		return Principal{}, err
	}
	roleIDs := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		roleIDs = append(roleIDs, assignment.RoleID)
	}

	return Principal{
		ID:          principal.ID,
		Kind:        principal.Kind,
		DisplayName: principal.DisplayName,
		Attributes:  principal.Attributes,
		RoleIDs:     roleIDs,
	}, nil
}

func (s *ManagementService) authkitManagementService() (*authkitmanagement.Service, error) {
	apiTokens, err := apikey.NewService(s.store)
	if err != nil {
		return nil, err
	}

	return authkitmanagement.NewService(authkitmanagement.Options{
		IdentityLinker:         s.store,
		APITokens:              apiTokens,
		APITokenMetadataLister: s.store,
	}), nil
}

func newAPITokenMetadata(token apikey.TokenMetadata) APITokenMetadata {
	return APITokenMetadata{
		ID:          token.ID,
		PrincipalID: token.PrincipalID,
		Name:        token.Name,
		ExpiresAt:   token.ExpiresAt,
		LastUsedAt:  token.LastUsedAt,
		RevokedAt:   token.RevokedAt,
	}
}

func builtinRoleDefinitions() []Role {
	return []Role{
		{
			ID:          RoleContentWriter,
			DisplayName: "Content writer",
			Description: "Can write imgsrv content.",
			Actions:     []string{ActionContentWrite},
		},
		{
			ID:          RoleAuthManager,
			DisplayName: "Auth manager",
			Description: "Can manage imgsrv authentication policy.",
			Actions:     []string{ActionAuthManage},
		},
	}
}

func validateBuiltinRoleID(roleID string) error {
	for _, role := range builtinRoleDefinitions() {
		if role.ID == roleID {
			return nil
		}
	}

	return fmt.Errorf("authz: unknown built-in role %q", roleID)
}

// UpdateOIDCProvisioningRule replaces a trusted provider and provisioning rule.
func (s *ManagementService) UpdateOIDCProvisioningRule(
	ctx context.Context,
	req SaveOIDCProvisioningRuleRequest,
) (OIDCProvisioningRule, error) {
	if s == nil || s.store == nil {
		return OIDCProvisioningRule{}, errManagementStoreRequired()
	}
	if _, err := s.store.FindProvisioningRule(ctx, req.ID); err != nil {
		return OIDCProvisioningRule{}, err
	}
	condition, err := managedRuleCondition(req.Audience, req.Condition)
	if err != nil {
		return OIDCProvisioningRule{}, err
	}

	provider, err := s.trustProvider(ctx, req)
	if err != nil {
		return OIDCProvisioningRule{}, err
	}
	if err = EnsureBuiltinRoles(ctx, s.store); err != nil {
		return OIDCProvisioningRule{}, err
	}

	rule, err := s.store.UpdateProvisioningRule(ctx, authkit.UpdateProvisioningRuleRequest{
		ID:            strings.TrimSpace(req.ID),
		DisplayName:   strings.TrimSpace(req.DisplayName),
		Provider:      provider.Issuer,
		Condition:     condition,
		AssignRoleIDs: []string{RoleContentWriter},
		Enabled:       req.Enabled,
	})
	if err != nil {
		return OIDCProvisioningRule{}, err
	}

	result := newOIDCProvisioningRule(rule, provider)
	s.logger.InfoContext(
		ctx,
		"oidc provisioning rule updated",
		"operation",
		"auth.update_oidc_provisioning_rule",
		"rule_id",
		result.ID,
		"issuer_url",
		result.IssuerURL,
		"enabled",
		result.Enabled,
		"forwarded_claim_count",
		len(result.ForwardedClaims),
	)

	return result, nil
}

// DeleteOIDCProvisioningRule deletes one OIDC provisioning rule.
func (s *ManagementService) DeleteOIDCProvisioningRule(ctx context.Context, id string) error {
	if s == nil || s.store == nil {
		return errManagementStoreRequired()
	}

	if err := s.store.DeleteProvisioningRule(ctx, id); err != nil {
		return err
	}
	s.logger.InfoContext(
		ctx,
		"oidc provisioning rule deleted",
		"operation",
		"auth.delete_oidc_provisioning_rule",
		"rule_id",
		id,
	)

	return nil
}

// PreviewOIDCProvisioningRuleReconciliation previews rule-granted role cleanup.
func (s *ManagementService) PreviewOIDCProvisioningRuleReconciliation(
	ctx context.Context,
	ruleID string,
) (OIDCProvisioningRuleReconciliation, error) {
	return s.oidcProvisioningRuleReconciliation(ctx, ruleID, false)
}

// ReconcileOIDCProvisioningRule removes rule-granted roles from existing principals.
func (s *ManagementService) ReconcileOIDCProvisioningRule(
	ctx context.Context,
	ruleID string,
) (OIDCProvisioningRuleReconciliation, error) {
	return s.oidcProvisioningRuleReconciliation(ctx, ruleID, true)
}

func (s *ManagementService) oidcProvisioningRuleReconciliation(
	ctx context.Context,
	ruleID string,
	apply bool,
) (OIDCProvisioningRuleReconciliation, error) {
	if s == nil || s.store == nil {
		return OIDCProvisioningRuleReconciliation{}, errManagementStoreRequired()
	}
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return OIDCProvisioningRuleReconciliation{}, errors.New("authz: OIDC provisioning rule ID is required")
	}

	result := OIDCProvisioningRuleReconciliation{
		RuleID:          ruleID,
		UnassignRoleIDs: []string{RoleContentWriter},
		Principals:      []Principal{},
		Applied:         apply,
	}
	principals, err := s.ListPrincipals(ctx)
	if err != nil {
		return OIDCProvisioningRuleReconciliation{}, err
	}
	for _, principal := range principals {
		if principal.Attributes["provisioning_rule_id"] != ruleID {
			continue
		}
		if !slices.Contains(principal.RoleIDs, RoleContentWriter) {
			continue
		}
		if apply {
			if err := s.store.UnassignPrincipalRole(ctx, authkit.UnassignPrincipalRoleRequest{
				PrincipalID: principal.ID,
				RoleID:      RoleContentWriter,
			}); err != nil {
				return OIDCProvisioningRuleReconciliation{}, err
			}
			principal.RoleIDs = withoutRole(principal.RoleIDs, RoleContentWriter)
		}
		result.Principals = append(result.Principals, principal)
	}

	s.logger.InfoContext(
		ctx,
		"oidc provisioning rule reconciliation evaluated",
		"operation",
		"auth.reconcile_oidc_provisioning_rule",
		"rule_id",
		result.RuleID,
		"applied",
		result.Applied,
		"principal_count",
		len(result.Principals),
		"role_count",
		len(result.UnassignRoleIDs),
	)

	return result, nil
}

// FindOIDCProvisioningRule returns one OIDC provisioning rule.
func (s *ManagementService) FindOIDCProvisioningRule(
	ctx context.Context,
	id string,
) (OIDCProvisioningRule, error) {
	if s == nil || s.store == nil {
		return OIDCProvisioningRule{}, errManagementStoreRequired()
	}

	rule, err := s.store.FindProvisioningRule(ctx, id)
	if err != nil {
		return OIDCProvisioningRule{}, err
	}

	return s.ruleWithProvider(ctx, rule)
}

// ListOIDCProvisioningRules returns OIDC provisioning rules in store order.
func (s *ManagementService) ListOIDCProvisioningRules(ctx context.Context) ([]OIDCProvisioningRule, error) {
	if s == nil || s.store == nil {
		return nil, errManagementStoreRequired()
	}

	rules, err := s.store.ListProvisioningRules(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]OIDCProvisioningRule, 0, len(rules))
	for _, rule := range rules {
		item, err := s.ruleWithProvider(ctx, rule)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	return result, nil
}

func (s *ManagementService) trustProvider(
	ctx context.Context,
	req SaveOIDCProvisioningRuleRequest,
) (authkitoidc.Provider, error) {
	provider, err := discoverProvider(
		ctx,
		s.httpClient,
		strings.TrimSpace(req.IssuerURL),
		[]string{strings.TrimSpace(req.Audience)},
		managedClaimPaths(req.ForwardedClaims),
	)
	if err != nil {
		return authkitoidc.Provider{}, err
	}
	existing, err := s.store.FindProvider(ctx, provider.Issuer)
	if err == nil {
		provider = mergeProviders([]authkitoidc.Provider{existing, provider})[0]
	} else if !errors.Is(err, authkitoidc.ErrProviderNotFound) {
		return authkitoidc.Provider{}, err
	}

	trusted, err := s.store.TrustProvider(ctx, provider)
	if err != nil {
		return authkitoidc.Provider{}, err
	}
	s.logger.DebugContext(
		ctx,
		"oidc provider trust updated",
		"operation",
		"auth.trust_oidc_provider",
		"issuer_url",
		trusted.Issuer,
		"audience_count",
		len(trusted.Audiences),
		"forwarded_claim_count",
		len(trusted.ForwardedClaims),
	)

	return trusted, nil
}

func (s *ManagementService) ruleWithProvider(
	ctx context.Context,
	rule authkit.ProvisioningRule,
) (OIDCProvisioningRule, error) {
	provider, err := s.store.FindProvider(ctx, rule.Provider)
	if err != nil {
		return OIDCProvisioningRule{}, err
	}

	return newOIDCProvisioningRule(rule, provider), nil
}

func newOIDCProvisioningRule(
	rule authkit.ProvisioningRule,
	provider authkitoidc.Provider,
) OIDCProvisioningRule {
	audience := firstString(provider.Audiences)
	condition := rule.Condition
	if parsedAudience, parsedCondition, ok := splitManagedRuleCondition(rule.Condition); ok {
		audience = parsedAudience
		condition = parsedCondition
	}

	return OIDCProvisioningRule{
		ID:              rule.ID,
		DisplayName:     rule.DisplayName,
		IssuerURL:       rule.Provider,
		Audience:        audience,
		ForwardedClaims: claimNames(provider.ForwardedClaims),
		Condition:       condition,
		AssignRoleIDs:   cloneStrings(rule.AssignRoleIDs),
		Enabled:         rule.Enabled,
	}
}

func managedRuleCondition(audience string, condition string) (string, error) {
	audience = strings.TrimSpace(audience)
	if audience == "" {
		return "", errors.New("authz: OIDC audience is required")
	}

	condition = provisioning.NormalizeCondition(condition)
	if err := provisioning.ValidateCondition(condition); err != nil {
		return "", err
	}

	guarded := fmt.Sprintf(
		"%s%s%s%s%s",
		managedAudienceConditionPrefix,
		strconv.Quote(audience),
		managedAudienceConditionMiddle,
		condition,
		managedAudienceConditionSuffix,
	)
	if err := provisioning.ValidateCondition(guarded); err != nil {
		return "", err
	}

	return guarded, nil
}

func splitManagedRuleCondition(condition string) (string, string, bool) {
	condition = provisioning.NormalizeCondition(condition)
	if !strings.HasPrefix(condition, managedAudienceConditionPrefix) {
		return "", "", false
	}

	rest := strings.TrimPrefix(condition, managedAudienceConditionPrefix)
	for end := 1; end <= len(rest); end++ {
		audience, err := strconv.Unquote(rest[:end])
		if err != nil || audience == "" {
			continue
		}
		if !strings.HasPrefix(rest[end:], managedAudienceConditionMiddle) {
			continue
		}

		operatorCondition := rest[end+len(managedAudienceConditionMiddle):]
		if !strings.HasSuffix(operatorCondition, managedAudienceConditionSuffix) {
			return "", "", false
		}
		operatorCondition = strings.TrimSuffix(operatorCondition, managedAudienceConditionSuffix)
		if operatorCondition == "" {
			return "", "", false
		}

		return audience, operatorCondition, true
	}

	return "", "", false
}

func claimPaths(claims []string) []authkit.ClaimPath {
	if len(claims) == 0 {
		return nil
	}
	result := make([]authkit.ClaimPath, 0, len(claims))
	for _, claim := range claims {
		claim = strings.TrimSpace(claim)
		if claim == "" {
			continue
		}
		result = append(result, authkit.ClaimPath{claim})
	}

	return result
}

func managedClaimPaths(claims []string) []authkit.ClaimPath {
	paths := claimPaths(claims)
	for _, path := range paths {
		if len(path) == 1 && path[0] == managedAudienceClaim {
			return paths
		}
	}

	return append(paths, authkit.ClaimPath{managedAudienceClaim})
}

func claimNames(paths []authkit.ClaimPath) []string {
	if len(paths) == 0 {
		return nil
	}
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if len(path) != 1 {
			continue
		}
		result = append(result, path[0])
	}

	return result
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func errManagementStoreRequired() error {
	return errors.New("authz: management store is required")
}

func withoutRole(roleIDs []string, roleID string) []string {
	filtered := make([]string, 0, len(roleIDs))
	for _, assigned := range roleIDs {
		if assigned != roleID {
			filtered = append(filtered, assigned)
		}
	}

	return filtered
}
