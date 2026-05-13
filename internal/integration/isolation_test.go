//go:build integration

package integration

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/integration/harness"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestSharedDependenciesIsolateDatabaseAndObjects(t *testing.T) {
	ctx := t.Context()
	firstEnv := startIntegrationEnv(t)
	secondEnv := startIntegrationEnv(t)
	firstClient := newClient(t, firstEnv)
	secondClient := newClient(t, secondEnv)
	imageName := "shared-deps-isolation"
	versionName := "v1.0.0"

	_, firstVersion := createDraftRelease(ctx, t, firstClient.Catalog(), imageName, versionName)
	_, secondVersion := createDraftRelease(ctx, t, secondClient.Catalog(), imageName, versionName)

	assert.Equal(t, versionName, firstVersion.Version)
	assert.Equal(t, versionName, secondVersion.Version)

	blob := uploadBlobToCAS(ctx, t, firstEnv, firstClient, []byte("shared dependency isolation payload"))
	digest, err := uploads.ParseDigest(blob.Digest.String())
	require.NoError(t, err)
	key := cas.StorageKey(digest)

	_, err = firstEnv.ObjectStore().StatObject(ctx, objectstore.StatObjectParams{Key: key})
	require.NoError(t, err)
	_, err = secondEnv.ObjectStore().StatObject(ctx, objectstore.StatObjectParams{Key: key})
	require.ErrorIs(t, err, objectstore.ErrNotFound)
}

func TestSharedDependenciesIsolateBootstrapState(t *testing.T) {
	var firstOutput bytes.Buffer
	firstEnv := startEnv(t, harness.WithBootstrapOutput(&firstOutput))
	require.NotEmpty(t, firstEnv.BaseURL())

	var secondOutput bytes.Buffer
	secondEnv := startEnv(t, harness.WithBootstrapOutput(&secondOutput))
	require.NotEmpty(t, secondEnv.BaseURL())

	require.NotEmpty(t, requireBootstrapToken(t, firstOutput.String()))
	require.NotEmpty(t, requireBootstrapToken(t, secondOutput.String()))
}
