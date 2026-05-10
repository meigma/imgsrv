package authz

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverProviderHonorsTimeout(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()

			return nil, req.Context().Err()
		}),
	}

	start := time.Now()
	_, err := discoverProviderWithTimeout(
		context.Background(),
		client,
		"https://issuer.example",
		[]string{"imgsrv-api"},
		nil,
		10*time.Millisecond,
	)

	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second)
}

func TestDiscoverProviderRejectsHTTPWithoutNetworkCall(t *testing.T) {
	called := false
	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true

			return nil, errors.New("unexpected network call")
		}),
	}

	_, err := discoverProviderWithTimeout(
		context.Background(),
		client,
		"http://issuer.example",
		[]string{"imgsrv-api"},
		nil,
		time.Second,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute HTTPS URL")
	assert.False(t, called)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
