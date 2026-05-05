package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	// problemMediaType is the RFC 9457 problem response Content-Type.
	problemMediaType = "application/problem+json"

	// defaultProblemType keeps the initial API contract conservative. Specific
	// problem type URIs should be added only when clients need stable branching
	// behavior beyond the HTTP status code.
	defaultProblemType = "about:blank"
)

// problemResponse is the wire shape of an RFC 9457 problem document emitted by the API.
type problemResponse struct {
	// Type is the problem type URI. defaultProblemType is used when no specific type applies.
	Type string `json:"type"`

	// Title is a short human-readable problem summary.
	Title string `json:"title"`

	// Status is the HTTP status code embedded in the problem body.
	Status int `json:"status"`

	// Detail is an optional human-readable occurrence detail.
	Detail string `json:"detail,omitempty"`

	// Instance optionally identifies this problem occurrence.
	Instance string `json:"instance,omitempty"`

	// Extensions carries problem-type-specific extension members merged at marshal time.
	Extensions map[string]json.RawMessage `json:"-"`
}

// MarshalJSON encodes the problem response and merges extension members alongside the RFC 9457 base fields.
func (problem problemResponse) MarshalJSON() ([]byte, error) {
	members := map[string]any{
		"type":   problem.Type,
		"title":  problem.Title,
		"status": problem.Status,
	}
	if problem.Detail != "" {
		members["detail"] = problem.Detail
	}
	if problem.Instance != "" {
		members["instance"] = problem.Instance
	}
	for key, value := range problem.Extensions {
		if isProblemBaseMember(key) {
			continue
		}
		members[key] = value
	}

	return json.Marshal(members)
}

// writeProblem writes an RFC 9457 problem response with the provided status and detail.
func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", problemMediaType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemResponse{
		Type:   defaultProblemType,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

// writeUploadError translates an uploads-package error into a problem response.
func writeUploadError(w http.ResponseWriter, err error) {
	writeProblem(w, uploadErrorStatus(err), err.Error())
}

// uploadErrorStatus maps an uploads-package sentinel error to an HTTP status code.
func uploadErrorStatus(err error) int {
	switch {
	case errors.Is(err, uploads.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, uploads.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, uploads.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, uploads.ErrFailedPrecondition):
		return http.StatusPreconditionFailed
	default:
		return http.StatusInternalServerError
	}
}

// isProblemBaseMember reports whether key names an RFC 9457 base problem member.
func isProblemBaseMember(key string) bool {
	switch key {
	case "type", "title", "status", "detail", "instance":
		return true
	default:
		return false
	}
}
