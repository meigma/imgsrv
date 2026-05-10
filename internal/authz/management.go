package authz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/meigma/authkit"
	authkitoidc "github.com/meigma/authkit/oidc"
	"github.com/meigma/authkit/provisioning"
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
}

// ManagementConfig configures a ManagementService.
type ManagementConfig struct {
	// Store persists trusted providers, provisioning rules, and built-in roles.
	Store ManagementStore

	// HTTPClient fetches OIDC discovery documents and JWKS keys. Nil uses a bounded default client.
	HTTPClient *http.Client
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

// NewManagementService constructs an auth-management service.
func NewManagementService(config ManagementConfig) *ManagementService {
	return &ManagementService{
		store:      config.Store,
		httpClient: config.HTTPClient,
	}
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

	return newOIDCProvisioningRule(rule, provider), nil
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

	return newOIDCProvisioningRule(rule, provider), nil
}

// DeleteOIDCProvisioningRule deletes one OIDC provisioning rule.
func (s *ManagementService) DeleteOIDCProvisioningRule(ctx context.Context, id string) error {
	if s == nil || s.store == nil {
		return errManagementStoreRequired()
	}

	return s.store.DeleteProvisioningRule(ctx, id)
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

	return s.store.TrustProvider(ctx, provider)
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
