package httpapi

import (
	"net/http"
	"time"

	"github.com/meigma/authkit"

	"github.com/meigma/imgsrv/internal/authz"
)

type roleResponse struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Actions     []string `json:"actions"`
}

type roleListResponse struct {
	Roles []roleResponse `json:"roles"`
}

type principalRequest struct {
	Kind        authkit.PrincipalKind `json:"kind"`
	DisplayName string                `json:"display_name"`
	Attributes  map[string]any        `json:"attributes,omitempty"`
}

type principalResponse struct {
	ID          string                `json:"id"`
	Kind        authkit.PrincipalKind `json:"kind"`
	DisplayName string                `json:"display_name"`
	Attributes  map[string]any        `json:"attributes,omitempty"`
	RoleIDs     []string              `json:"role_ids"`
}

type principalListResponse struct {
	Principals []principalResponse `json:"principals"`
}

type issueAPITokenRequest struct {
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at"`
}

type apiTokenResponse struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principal_id"`
	Name        string     `json:"name"`
	ExpiresAt   time.Time  `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	Plaintext   string     `json:"plaintext,omitempty"`
}

type apiTokenListResponse struct {
	APITokens []apiTokenResponse `json:"api_tokens"`
}

func (a *api) listAuthRoles(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	roles, err := service.ListRoles(r.Context())
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newRoleListResponse(roles))
}

func (a *api) createAuthPrincipal(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	var request principalRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	principal, err := service.CreatePrincipal(r.Context(), authz.CreatePrincipalRequest{
		Kind:        request.Kind,
		DisplayName: request.DisplayName,
		Attributes:  request.Attributes,
	})
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, newPrincipalResponse(principal))
}

func (a *api) listAuthPrincipals(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	principals, err := service.ListPrincipals(r.Context())
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newPrincipalListResponse(principals))
}

func (a *api) getAuthPrincipal(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	principal, err := service.FindPrincipal(r.Context(), r.PathValue("principal_id"))
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newPrincipalResponse(principal))
}

func (a *api) assignAuthPrincipalRole(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	if err := service.AssignPrincipalRole(
		r.Context(),
		r.PathValue("principal_id"),
		r.PathValue("role_id"),
	); err != nil {
		writeAuthManagementError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *api) unassignAuthPrincipalRole(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	if err := service.UnassignPrincipalRole(
		r.Context(),
		r.PathValue("principal_id"),
		r.PathValue("role_id"),
	); err != nil {
		writeAuthManagementError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *api) issueAuthPrincipalAPIToken(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	var request issueAPITokenRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	issued, err := service.IssueAPIToken(r.Context(), authz.IssueAPITokenRequest{
		PrincipalID: r.PathValue("principal_id"),
		Name:        request.Name,
		ExpiresAt:   request.ExpiresAt,
	})
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, newIssuedAPITokenResponse(issued))
}

func (a *api) listAuthPrincipalAPITokens(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	tokens, err := service.ListPrincipalAPITokens(r.Context(), r.PathValue("principal_id"))
	if err != nil {
		writeAuthManagementError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newAPITokenListResponse(tokens))
}

func (a *api) revokeAuthAPIToken(w http.ResponseWriter, r *http.Request) {
	service := a.authMgmt
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, errAuthManagementUnavailable.Error())
		return
	}

	if err := service.RevokeAPIToken(r.Context(), r.PathValue("token_id")); err != nil {
		writeAuthManagementError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func newRoleListResponse(roles []authz.Role) roleListResponse {
	response := roleListResponse{
		Roles: make([]roleResponse, 0, len(roles)),
	}
	for _, role := range roles {
		response.Roles = append(response.Roles, roleResponse{
			ID:          role.ID,
			DisplayName: role.DisplayName,
			Description: role.Description,
			Actions:     role.Actions,
		})
	}

	return response
}

func newPrincipalListResponse(principals []authz.Principal) principalListResponse {
	response := principalListResponse{
		Principals: make([]principalResponse, 0, len(principals)),
	}
	for _, principal := range principals {
		response.Principals = append(response.Principals, newPrincipalResponse(principal))
	}

	return response
}

func newPrincipalResponse(principal authz.Principal) principalResponse {
	return principalResponse{
		ID:          principal.ID,
		Kind:        principal.Kind,
		DisplayName: principal.DisplayName,
		Attributes:  principal.Attributes,
		RoleIDs:     principal.RoleIDs,
	}
}

func newAPITokenListResponse(tokens []authz.APITokenMetadata) apiTokenListResponse {
	response := apiTokenListResponse{
		APITokens: make([]apiTokenResponse, 0, len(tokens)),
	}
	for _, token := range tokens {
		response.APITokens = append(response.APITokens, newAPITokenResponse(token))
	}

	return response
}

func newIssuedAPITokenResponse(token authz.IssuedAPIToken) apiTokenResponse {
	response := newAPITokenResponse(token.APITokenMetadata)
	response.Plaintext = token.Plaintext

	return response
}

func newAPITokenResponse(token authz.APITokenMetadata) apiTokenResponse {
	return apiTokenResponse{
		ID:          token.ID,
		PrincipalID: token.PrincipalID,
		Name:        token.Name,
		ExpiresAt:   token.ExpiresAt,
		LastUsedAt:  token.LastUsedAt,
		RevokedAt:   token.RevokedAt,
	}
}
