package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const (
	// defaultUserAgent is the User-Agent used when Options.UserAgent is empty.
	defaultUserAgent = "imgsrv-go-client"

	// maxErrorBodyBytes caps how much of an error response body is buffered for
	// diagnostics so a hostile or oversized payload cannot exhaust client memory.
	maxErrorBodyBytes = 1 << 20

	// problemJSONContentType is the RFC 9457 media type for problem responses.
	problemJSONContentType = "application/problem+json"
)

// transport carries the resolved HTTP configuration shared by every operation
// group and performs JSON-aware request and response handling.
type transport struct {
	baseURL     *url.URL
	httpClient  *http.Client
	bearerToken string
	userAgent   string
}

// newTransport validates options and returns a transport with defaults applied
// for the HTTP client and User-Agent.
func newTransport(options Options) (*transport, error) {
	baseURL, err := parseBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	userAgent := options.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	return &transport{
		baseURL:     baseURL,
		httpClient:  httpClient,
		bearerToken: options.BearerToken,
		userAgent:   userAgent,
	}, nil
}

// parseBaseURL returns the normalized base URL for the imgsrv API.
//
// The URL must be non-empty, use http or https, include a host, and carry no
// query or fragment. Any trailing slash on the path is removed so callers can
// concatenate operation paths that always begin with a slash.
func parseBaseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("imgsrv client base url is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse imgsrv client base url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("imgsrv client base url must use http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("imgsrv client base url must include a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("imgsrv client base url must not include query or fragment")
	}

	normalized := *parsed
	normalized.Path = strings.TrimRight(normalized.Path, "/")

	return &normalized, nil
}

// doJSON marshals requestBody as JSON, sets the JSON content type, and delegates
// to do for transport, status checking, and response decoding.
func (transport *transport) doJSON(
	ctx context.Context,
	path string,
	requestBody any,
	wantStatus int,
	responseBody any,
) error {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("encode imgsrv request: %w", err)
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")

	return transport.do(
		ctx,
		http.MethodPost,
		path,
		bytes.NewReader(body),
		int64(len(body)),
		headers,
		wantStatus,
		responseBody,
	)
}

// do issues a single HTTP request, returns a typed error when the response
// status does not match wantStatus, and otherwise decodes the response body
// into responseBody when one is provided.
func (transport *transport) do(
	ctx context.Context,
	method string,
	path string,
	body io.Reader,
	contentLength int64,
	headers http.Header,
	wantStatus int,
	responseBody any,
) error {
	req, err := http.NewRequestWithContext(ctx, method, transport.endpoint(path), body)
	if err != nil {
		return fmt.Errorf("create imgsrv request: %w", err)
	}
	if body != nil {
		req.ContentLength = contentLength
	}
	transport.prepareRequest(req, headers)

	resp, err := transport.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send imgsrv request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != wantStatus {
		return decodeErrorResponse(resp)
	}
	if responseBody == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode imgsrv response: %w", err)
	}

	return nil
}

// endpoint joins the configured base URL with the operation path.
func (transport *transport) endpoint(path string) string {
	next := *transport.baseURL
	next.Path = transport.baseURL.Path + path

	return next.String()
}

// prepareRequest applies the default Accept, User-Agent, and bearer token
// headers, then overlays caller-supplied headers so per-call values win.
func (transport *transport) prepareRequest(req *http.Request, headers http.Header) {
	req.Header.Set("Accept", "application/json, application/problem+json")
	req.Header.Set("User-Agent", transport.userAgent)
	if transport.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+transport.bearerToken)
	}
	for key, values := range headers {
		req.Header.Del(key)
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
}

// decodeErrorResponse converts a non-success HTTP response into a ProblemError
// when the body is RFC 9457 problem JSON, or an HTTPError otherwise.
func decodeErrorResponse(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return fmt.Errorf("read imgsrv error response: %w", err)
	}

	if responseIsProblemJSON(resp.Header.Get("Content-Type")) {
		var problem ProblemError
		if err := json.Unmarshal(body, &problem); err == nil {
			problem.HTTPStatus = resp.StatusCode
			if problem.Type == "" {
				problem.Type = "about:blank"
			}
			return &problem
		}
	}

	return &HTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       body,
	}
}

// responseIsProblemJSON reports whether the Content-Type identifies an RFC 9457
// problem JSON body.
func responseIsProblemJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	return mediaType == problemJSONContentType
}
