// Package client provides a handwritten Go SDK for the imgsrv HTTP API.
package client

// Client is the concrete root imgsrv SDK client.
//
// Code that needs a mockable dependency should prefer narrow operation-group
// interfaces such as UploadsClient instead of depending on Client directly.
type Client struct {
	// auth holds the HTTP-backed auth-management operation group.
	auth *HTTPAuthClient

	// blobs holds the HTTP-backed CAS blob operation group.
	blobs *HTTPBlobsClient

	// catalog holds the HTTP-backed catalog operation group.
	catalog *HTTPCatalogClient

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
		auth:    newHTTPAuthClient(transport),
		blobs:   newHTTPBlobsClient(transport),
		catalog: newHTTPCatalogClient(transport),
		uploads: newHTTPUploadsClient(transport),
	}, nil
}

// Auth returns auth-management API operations.
func (client *Client) Auth() AuthClient {
	if client == nil {
		return nil
	}

	return client.auth
}

// Blobs returns raw CAS blob API operations.
func (client *Client) Blobs() BlobsClient {
	if client == nil {
		return nil
	}

	return client.blobs
}

// Catalog returns image catalog API operations.
func (client *Client) Catalog() CatalogClient {
	if client == nil {
		return nil
	}

	return client.catalog
}

// Uploads returns upload API operations.
func (client *Client) Uploads() UploadsClient {
	if client == nil {
		return nil
	}

	return client.uploads
}
