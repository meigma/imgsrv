package client

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
)

// ProblemError is an RFC 9457 problem response returned by imgsrv.
type ProblemError struct {
	// HTTPStatus is the actual HTTP response status code.
	HTTPStatus int `json:"-"`

	// Type is the problem type URI. Empty means the server omitted it, which RFC 9457 treats as about:blank.
	Type string `json:"type,omitempty"`

	// Title is a short human-readable problem summary.
	Title string `json:"title,omitempty"`

	// Status is the advisory HTTP status embedded in the problem body.
	Status int `json:"status,omitempty"`

	// Detail is a human-readable occurrence detail. Client code must not parse it.
	Detail string `json:"detail,omitempty"`

	// Instance identifies this problem occurrence when the server provides one.
	Instance string `json:"instance,omitempty"`

	// Extensions preserves problem-type-specific extension members.
	Extensions map[string]json.RawMessage `json:"-"`
}

// Error returns a compact human-readable problem summary.
func (err *ProblemError) Error() string {
	if err == nil {
		return "<nil>"
	}

	status := err.HTTPStatus
	if status == 0 {
		status = err.Status
	}
	title := err.Title
	if title == "" {
		title = http.StatusText(status)
	}
	if err.Detail != "" {
		return fmt.Sprintf("imgsrv: %d %s: %s", status, title, err.Detail)
	}

	return fmt.Sprintf("imgsrv: %d %s", status, title)
}

// UnmarshalJSON decodes an RFC 9457 problem object while ignoring malformed known members.
func (err *ProblemError) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if decodeErr := json.Unmarshal(data, &raw); decodeErr != nil {
		return decodeErr
	}

	err.Type = decodeStringMember(raw, "type")
	err.Title = decodeStringMember(raw, "title")
	err.Status = decodeIntMember(raw, "status")
	err.Detail = decodeStringMember(raw, "detail")
	err.Instance = decodeStringMember(raw, "instance")
	err.Extensions = problemExtensions(raw)

	return nil
}

// HTTPError describes a non-problem HTTP error response.
type HTTPError struct {
	// StatusCode is the HTTP response status code.
	StatusCode int

	// Status is the HTTP response status line text.
	Status string

	// Body contains a bounded copy of the response body.
	Body []byte
}

// Error returns a compact human-readable HTTP error summary.
func (err *HTTPError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if len(err.Body) == 0 {
		return fmt.Sprintf("imgsrv: %s", err.Status)
	}

	return fmt.Sprintf("imgsrv: %s: %s", err.Status, strings.TrimSpace(string(err.Body)))
}

// decodeStringMember returns the named member decoded as a string, or the empty
// string when the member is absent or not a JSON string.
func decodeStringMember(raw map[string]json.RawMessage, name string) string {
	var value string
	if err := json.Unmarshal(raw[name], &value); err != nil {
		return ""
	}

	return value
}

// decodeIntMember returns the named member decoded as an int, or zero when the
// member is absent or not a JSON number.
func decodeIntMember(raw map[string]json.RawMessage, name string) int {
	var value int
	if err := json.Unmarshal(raw[name], &value); err != nil {
		return 0
	}

	return value
}

// problemExtensions returns the raw object members that are not RFC 9457 known
// fields, or nil when no extension members are present.
func problemExtensions(raw map[string]json.RawMessage) map[string]json.RawMessage {
	extensions := maps.Clone(raw)
	delete(extensions, "type")
	delete(extensions, "title")
	delete(extensions, "status")
	delete(extensions, "detail")
	delete(extensions, "instance")
	if len(extensions) == 0 {
		return nil
	}

	return extensions
}
