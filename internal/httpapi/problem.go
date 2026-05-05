package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/meigma/imgsrv/internal/uploads"
)

const problemMediaType = "application/problem+json"

type problemResponse struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", problemMediaType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemResponse{
		Type:   "about:blank",
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
