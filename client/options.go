package client

import "net/http"

// Options configures an imgsrv API client.
type Options struct {
	// BaseURL is the root URL for the imgsrv HTTP API.
	BaseURL string

	// HTTPClient sends HTTP requests. Nil selects http.DefaultClient.
	HTTPClient *http.Client

	// BearerToken is sent as an Authorization bearer token when set.
	BearerToken string

	// UserAgent is sent as the User-Agent header. Empty selects a default.
	UserAgent string
}
