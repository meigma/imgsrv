package httpapi

import (
	"errors"
	"net/http"
)

// errSimpleStreamsServiceUnavailable signals that Simple Streams routes were called without a configured service.
var errSimpleStreamsServiceUnavailable = errors.New("incus simplestreams service is not configured")

// simpleStreamsIndex handles GET /streams/v1/index.json.
func (a *api) simpleStreamsIndex(w http.ResponseWriter, r *http.Request) {
	service, ok := a.simpleStreamsService(w)
	if !ok {
		return
	}

	body, err := service.Index(r.Context())
	if err != nil {
		a.logger.ErrorContext(
			r.Context(),
			"simple streams index render failed",
			"operation",
			"simplestreams.index",
			"request_id",
			RequestIDFromContext(r.Context()),
			"error",
			err,
		)
		writeProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.logger.DebugContext(
		r.Context(),
		"simple streams index rendered",
		"operation",
		"simplestreams.index",
		"request_id",
		RequestIDFromContext(r.Context()),
		"bytes",
		len(body),
	)

	writeSimpleStreamsJSON(w, body)
}

// simpleStreamsProductFile handles GET /streams/v1/images.json.
func (a *api) simpleStreamsProductFile(w http.ResponseWriter, r *http.Request) {
	service, ok := a.simpleStreamsService(w)
	if !ok {
		return
	}

	body, err := service.ProductFile(r.Context())
	if err != nil {
		a.logger.ErrorContext(
			r.Context(),
			"simple streams product render failed",
			"operation",
			"simplestreams.product_file",
			"request_id",
			RequestIDFromContext(r.Context()),
			"error",
			err,
		)
		writeProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.logger.DebugContext(
		r.Context(),
		"simple streams product rendered",
		"operation",
		"simplestreams.product_file",
		"request_id",
		RequestIDFromContext(r.Context()),
		"bytes",
		len(body),
	)

	writeSimpleStreamsJSON(w, body)
}

// simpleStreamsService returns the configured SimpleStreamsService or writes a 503 problem.
func (a *api) simpleStreamsService(w http.ResponseWriter) (SimpleStreamsService, bool) {
	if a.streams == nil {
		writeProblem(w, http.StatusServiceUnavailable, errSimpleStreamsServiceUnavailable.Error())
		return nil, false
	}

	return a.streams, true
}

// writeSimpleStreamsJSON writes a pre-rendered Simple Streams JSON document.
func writeSimpleStreamsJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
