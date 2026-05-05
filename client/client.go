// Package client provides a handwritten Go SDK for the imgsrv HTTP API.
package client

// Client is the concrete root imgsrv SDK client.
//
// Code that needs a mockable dependency should prefer narrow operation-group
// interfaces such as UploadsClient instead of depending on Client directly.
type Client struct {
	// uploads holds the HTTP-backed upload operation group.
	uploads *HTTPUploadsClient
}

// New constructs an HTTP-backed imgsrv API client.
func New(options Options) (*Client, error) {
	transport, err := newTransport(options)
	if err != nil {
		return nil, err
	}

	return &Client{
		uploads: newHTTPUploadsClient(transport),
	}, nil
}

// Uploads returns upload API operations.
func (client *Client) Uploads() UploadsClient {
	if client == nil {
		return nil
	}

	return client.uploads
}
