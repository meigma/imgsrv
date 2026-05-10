package httpapi

import (
	"errors"
	"net/http"

	"github.com/meigma/authkit"

	"github.com/meigma/imgsrv/internal/authz"
)

var errAuthManagementUnavailable = errors.New("auth management service is not configured")

type oidcProvisioningRuleRequest struct {
	ID              string   `json:"id,omitempty"`
	DisplayName     string   `json:"display_name"`
	IssuerURL       string   `json:"issuer_url"`
	Audience        string   `json:"audience"`
	ForwardedClaims []string `json:"forwarded_claims,omitempty"`
	Condition       string   `json:"condition"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type oidcProvisioningRuleResponse struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	IssuerURL       string   `json:"issuer_url"`
	Audience        string   `json:"audience"`
	ForwardedClaims []string `json:"forwarded_claims,omitempty"`
	Condition       string   `json:"condition"`
	AssignRoleIDs   []string `json:"assign_role_ids"`
	Enabled         bool     `json:"enabled"`
}

type oidcProvisioningRuleListResponse struct {
	Rules []oidcProvisioningRuleResponse `json:"rules"`
}

func (a *api) createOIDCProvisioningRule(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	var request oidcProvisioningRuleRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	rule, err := service.CreateOIDCProvisioningRule(r.Context(), saveOIDCProvisioningRuleRequest("", request))
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, newOIDCProvisioningRuleResponse(rule))
}

func (a *api) listOIDCProvisioningRules(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	rules, err := service.ListOIDCProvisioningRules(r.Context())
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newOIDCProvisioningRuleListResponse(rules))
}

func (a *api) getOIDCProvisioningRule(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	rule, err := service.FindOIDCProvisioningRule(r.Context(), r.PathValue("rule_id"))
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newOIDCProvisioningRuleResponse(rule))
}

func (a *api) updateOIDCProvisioningRule(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	var request oidcProvisioningRuleRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	rule, err := service.UpdateOIDCProvisioningRule(
		r.Context(),
		saveOIDCProvisioningRuleRequest(r.PathValue("rule_id"), request),
	)
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newOIDCProvisioningRuleResponse(rule))
}

func (a *api) deleteOIDCProvisioningRule(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	if err := service.DeleteOIDCProvisioningRule(r.Context(), r.PathValue("rule_id")); err != nil {
		writeAuthManagementError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func saveOIDCProvisioningRuleRequest(
	pathID string,
	request oidcProvisioningRuleRequest,
) authz.SaveOIDCProvisioningRuleRequest {
	id := pathID
	if id == "" {
		id = request.ID
	}

	return authz.SaveOIDCProvisioningRuleRequest{
		ID:              id,
		DisplayName:     request.DisplayName,
		IssuerURL:       request.IssuerURL,
		Audience:        request.Audience,
		ForwardedClaims: request.ForwardedClaims,
		Condition:       request.Condition,
		Enabled:         enabledValue(request.Enabled),
	}
}

func enabledValue(value *bool) bool {
	return value == nil || *value
}

func newOIDCProvisioningRuleListResponse(
	rules []authz.OIDCProvisioningRule,
) oidcProvisioningRuleListResponse {
	response := oidcProvisioningRuleListResponse{
		Rules: make([]oidcProvisioningRuleResponse, 0, len(rules)),
	}
	for _, rule := range rules {
		response.Rules = append(response.Rules, newOIDCProvisioningRuleResponse(rule))
	}

	return response
}

func newOIDCProvisioningRuleResponse(rule authz.OIDCProvisioningRule) oidcProvisioningRuleResponse {
	return oidcProvisioningRuleResponse{
		ID:              rule.ID,
		DisplayName:     rule.DisplayName,
		IssuerURL:       rule.IssuerURL,
		Audience:        rule.Audience,
		ForwardedClaims: rule.ForwardedClaims,
		Condition:       rule.Condition,
		AssignRoleIDs:   rule.AssignRoleIDs,
		Enabled:         rule.Enabled,
	}
}

func writeAuthManagementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authkit.ErrProvisioningRuleNotFound):
		writeProblem(w, http.StatusNotFound, err.Error())
	default:
		writeProblem(w, http.StatusBadRequest, err.Error())
	}
}
