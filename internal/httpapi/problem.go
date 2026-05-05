package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	problemMediaType = "application/problem+json"

	// defaultProblemType keeps the initial API contract conservative. Specific
	// problem type URIs should be added only when clients need stable branching
	// behavior beyond the HTTP status code.
	defaultProblemType = "about:blank"
)

type problemResponse struct {
	Type       string                     `json:"type"`
	Title      string                     `json:"title"`
	Status     int                        `json:"status"`
	Detail     string                     `json:"detail,omitempty"`
	Instance   string                     `json:"instance,omitempty"`
	Extensions map[string]json.RawMessage `json:"-"`
}

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

func writeUploadError(w http.ResponseWriter, err error) {
	writeProblem(w, uploadErrorStatus(err), err.Error())
}

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

func isProblemBaseMember(key string) bool {
	switch key {
	case "type", "title", "status", "detail", "instance":
		return true
	default:
		return false
	}
}
