//go:build integration

package imgsrvtest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	imgsrvtest "github.com/meigma/imgsrv/test"
)

func TestStartProvidesSDKClient(t *testing.T) {
	env := imgsrvtest.Start(t, imgsrvtest.WithAPIToken())

	require.NotEmpty(t, env.BaseURL())
	require.Same(t, env.HTTPClient(), env.ClientOptions().HTTPClient)

	image, err := env.Client(t).Catalog().CreateImage(t.Context(), imgsrv.CreateImageRequest{
		Name: "imgsrvtest-harness-smoke",
	})

	require.NoError(t, err)
	assert.Equal(t, "imgsrvtest-harness-smoke", image.Name)
}
