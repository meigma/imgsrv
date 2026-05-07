package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	httpmocks "github.com/meigma/imgsrv/internal/httpapi/mocks"
	"github.com/meigma/imgsrv/internal/objectstore"
)

func TestCreateImageCreatesNamespace(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	displayName := "Debian 12"
	description := "Base image"
	wantImage := catalogImageFixture()
	wantImage.DisplayName = &displayName
	wantImage.Description = &description

	tc.catalog.EXPECT().
		CreateImage(mock.Anything, mock.MatchedBy(func(params catalog.CreateImageParams) bool {
			return params.Name == "debian" &&
				params.DisplayName != nil &&
				*params.DisplayName == displayName &&
				params.Description != nil &&
				*params.Description == description
		})).
		Return(wantImage, nil)

	req := newHTTPAPIRequest(http.MethodPost, "/v1/images", strings.NewReader(`{
		"name": "debian",
		"display_name": "Debian 12",
		"description": "Base image"
	}`))
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var got imageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, catalogImageIDFixture().String(), got.ID)
	assert.Equal(t, "debian", got.Name)
	require.NotNil(t, got.DisplayName)
	assert.Equal(t, displayName, *got.DisplayName)
	require.NotNil(t, got.Description)
	assert.Equal(t, description, *got.Description)
}

func TestListImagesReturnsNamespaces(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		ListImages(mock.Anything, catalog.ListImagesParams{}).
		Return([]catalog.Image{catalogImageFixture()}, nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got imageListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Images, 1)
	assert.Equal(t, "debian", got.Images[0].Name)
}

func TestGetImageReturnsNamespace(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		GetImage(mock.Anything, catalog.GetImageParams{Name: "debian"}).
		Return(catalogImageFixture(), nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images/debian", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got imageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "debian", got.Name)
}

func TestCreateDraftVersionCreatesVersionUnderImage(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	wantVersion := catalogVersionFixture(catalog.VersionStateDraft)

	tc.catalog.EXPECT().
		CreateDraftVersion(mock.Anything, catalog.CreateDraftVersionParams{
			ImageName: "debian",
			Version:   "v1.0.0",
		}).
		Return(wantVersion, nil)

	req := newHTTPAPIRequest(http.MethodPost, "/v1/images/debian/versions", strings.NewReader(`{
		"version": "v1.0.0"
	}`))
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var got versionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, catalogVersionIDFixture().String(), got.ID)
	assert.Equal(t, catalogImageIDFixture().String(), got.ImageID)
	assert.Equal(t, "v1.0.0", got.Version)
	assert.Equal(t, catalog.VersionStateDraft, got.State)
	assert.Nil(t, got.PublishedAt)
}

func TestListVersionsReturnsImageVersions(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		ListVersions(mock.Anything, catalog.ListVersionsParams{ImageName: "debian"}).
		Return([]catalog.Version{catalogVersionFixture(catalog.VersionStateDraft)}, nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images/debian/versions", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got versionListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Versions, 1)
	assert.Equal(t, "v1.0.0", got.Versions[0].Version)
	assert.Equal(t, catalog.VersionStateDraft, got.Versions[0].State)
}

func TestGetVersionManifestReturnsManifest(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	wantManifest := catalogManifestFixture(catalog.VersionStatePublished)

	tc.catalog.EXPECT().
		GetVersionManifest(mock.Anything, catalog.GetVersionManifestParams{
			ImageName: "debian",
			Version:   "v1.0.0",
		}).
		Return(wantManifest, nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images/debian/versions/v1.0.0", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got manifestResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "debian", got.Image.Name)
	assert.Equal(t, "v1.0.0", got.Version.Version)
	assert.Equal(t, catalog.VersionStatePublished, got.Version.State)
	require.Len(t, got.Artifacts, 1)
	assert.Equal(t, catalogArtifactIDFixture().String(), got.Artifacts[0].Artifact.ID)
	require.Len(t, got.Artifacts[0].Attachments, 1)
	assert.Equal(t, catalogAttachmentIDFixture().String(), got.Artifacts[0].Attachments[0].ID)
}

func TestListPublishedArtifactsReturnsArtifacts(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		ListPublishedArtifacts(mock.Anything, catalog.ListPublishedArtifactsParams{
			ImageName: "debian",
			Version:   "v1.0.0",
		}).
		Return([]catalog.Artifact{catalogArtifactFixture()}, nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images/debian/versions/v1.0.0/artifacts", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got artifactListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Artifacts, 1)
	assert.Equal(t, catalogArtifactIDFixture().String(), got.Artifacts[0].ID)
	assert.Equal(t, catalogDigestFixture().String(), got.Artifacts[0].PrimaryBlobDigest)
}

func TestGetPublishedArtifactReturnsArtifact(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		GetPublishedArtifact(mock.Anything, catalog.GetPublishedArtifactParams{
			ImageName:  "debian",
			Version:    "v1.0.0",
			ArtifactID: catalogArtifactIDFixture(),
		}).
		Return(catalogArtifactFixture(), nil)

	req := newHTTPAPIRequest(
		http.MethodGet,
		"/v1/images/debian/versions/v1.0.0/artifacts/"+catalogArtifactIDFixture().String(),
		nil,
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got artifactResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, catalogArtifactIDFixture().String(), got.ID)
	assert.Equal(t, "application/gzip", got.PrimaryMediaType)
}

func TestDownloadPublishedArtifactStreamsBlobWithCatalogMediaType(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	body := "artifact-bytes"
	artifact := catalogArtifactFixture()
	artifact.PrimaryBlobSizeBytes = int64(len(body))
	blob := catalogArtifactBlob(artifact)

	tc.catalog.EXPECT().
		GetPublishedArtifact(mock.Anything, catalog.GetPublishedArtifactParams{
			ImageName:  "debian",
			Version:    "v1.0.0",
			ArtifactID: catalogArtifactIDFixture(),
		}).
		Return(artifact, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(blobReaderFixture(blob, body), nil)

	req := newHTTPAPIRequest(
		http.MethodGet,
		"/v1/images/debian/versions/v1.0.0/artifacts/"+catalogArtifactIDFixture().String()+"/download",
		nil,
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/gzip", rec.Header().Get("Content-Type"))
	assert.Equal(t, blobETag(blob), rec.Header().Get("ETag"))
	assert.Equal(t, "14", rec.Header().Get("Content-Length"))
	assert.Equal(t, body, rec.Body.String())
}

func TestDownloadPublishedArtifactSupportsRangeAndHead(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		rangeValue string
		wantStatus int
		wantLength string
		wantRange  string
		wantBody   string
	}{
		{
			name:       "range",
			method:     http.MethodGet,
			rangeValue: "bytes=2-4",
			wantStatus: http.StatusPartialContent,
			wantLength: "3",
			wantRange:  "bytes 2-4/10",
			wantBody:   "234",
		},
		{
			name:       "head",
			method:     http.MethodHead,
			wantStatus: http.StatusOK,
			wantLength: "10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newCatalogHandlerTestContext(t)
			body := blobPayload
			if tt.rangeValue != "" {
				body = tt.wantBody
			}
			artifact := catalogArtifactFixture()
			artifact.PrimaryBlobSizeBytes = int64(len(blobPayload))
			blob := catalogArtifactBlob(artifact)

			tc.catalog.EXPECT().
				GetPublishedArtifact(mock.Anything, catalog.GetPublishedArtifactParams{
					ImageName:  "debian",
					Version:    "v1.0.0",
					ArtifactID: catalogArtifactIDFixture(),
				}).
				Return(artifact, nil)
			expectedOpen := cas.OpenBlobParams{Digest: blob.Digest}
			if tt.rangeValue != "" {
				expectedOpen.Range = rangePtr(2, 4)
			}
			tc.blobs.EXPECT().
				OpenBlob(mock.Anything, expectedOpen).
				Return(blobReaderFixture(blob, body), nil)

			req := newHTTPAPIRequest(
				tt.method,
				"/v1/images/debian/versions/v1.0.0/artifacts/"+catalogArtifactIDFixture().String()+"/download",
				nil,
			)
			if tt.rangeValue != "" {
				req.Header.Set("Range", tt.rangeValue)
			}
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantLength, rec.Header().Get("Content-Length"))
			assert.Equal(t, tt.wantRange, rec.Header().Get("Content-Range"))
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestDownloadPublishedAttachmentStreamsAttachmentBlob(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	body := "checksum"
	attachment := catalogAttachmentFixture()
	attachment.BlobSizeBytes = int64(len(body))
	blob := catalogAttachmentBlob(attachment)

	tc.catalog.EXPECT().
		GetPublishedAttachment(mock.Anything, catalog.GetPublishedAttachmentParams{
			ImageName:    "debian",
			Version:      "v1.0.0",
			ArtifactID:   catalogArtifactIDFixture(),
			AttachmentID: catalogAttachmentIDFixture(),
		}).
		Return(attachment, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(blobReaderFixture(blob, body), nil)

	req := newHTTPAPIRequest(
		http.MethodGet,
		"/v1/images/debian/versions/v1.0.0/artifacts/"+
			catalogArtifactIDFixture().String()+
			"/attachments/"+
			catalogAttachmentIDFixture().String()+
			"/download",
		nil,
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
	assert.Equal(t, "8", rec.Header().Get("Content-Length"))
	assert.Equal(t, body, rec.Body.String())
}

func TestDownloadPublishedArtifactMapsMissingCASBlobToNotFound(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	artifact := catalogArtifactFixture()
	blob := catalogArtifactBlob(artifact)

	tc.catalog.EXPECT().
		GetPublishedArtifact(mock.Anything, mock.Anything).
		Return(artifact, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(blobReaderFixture(blob, ""), cas.ErrNotFound)

	req := newHTTPAPIRequest(
		http.MethodGet,
		"/v1/images/debian/versions/v1.0.0/artifacts/"+catalogArtifactIDFixture().String()+"/download",
		nil,
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assertProblem(t, rec, http.StatusNotFound, cas.ErrNotFound.Error())
}

func TestResolveManifestReturnsPublishedManifestForRef(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	wantManifest := catalogManifestFixture(catalog.VersionStatePublished)

	tc.catalog.EXPECT().
		ResolveManifest(mock.Anything, catalog.ResolveManifestParams{
			ImageName: "debian",
			Version:   "latest",
		}).
		Return(wantManifest, nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images/debian/refs/latest", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got manifestResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "debian", got.Image.Name)
	assert.Equal(t, "v1.0.0", got.Version.Version)
	assert.Equal(t, catalog.VersionStatePublished, got.Version.State)
}

func TestAddArtifactCreatesPrimaryArtifact(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	wantArtifact := catalogArtifactFixture()

	tc.catalog.EXPECT().
		AddArtifact(mock.Anything, catalog.AddArtifactParams{
			ImageName:            "debian",
			Version:              "v1.0.0",
			OperatingSystem:      "linux",
			Architecture:         "x86_64",
			Format:               catalog.ArtifactFormatRawGZ,
			PrimaryBlobDigest:    catalogDigestFixture(),
			PrimaryBlobSizeBytes: 1024,
			PrimaryMediaType:     "application/gzip",
		}).
		Return(wantArtifact, nil)

	req := newHTTPAPIRequest(http.MethodPost, "/v1/images/debian/versions/v1.0.0/artifacts", strings.NewReader(`{
		"operating_system": "linux",
		"architecture": "x86_64",
		"format": "raw.gz",
		"primary_blob_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"primary_blob_size_bytes": 1024,
		"primary_media_type": "application/gzip"
	}`))
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var got artifactResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, catalogArtifactIDFixture().String(), got.ID)
	assert.Equal(t, "linux", got.OperatingSystem)
	assert.Equal(t, "x86_64", got.Architecture)
	assert.Equal(t, catalog.ArtifactFormatRawGZ, got.Format)
	assert.Equal(t, catalogDigestFixture().String(), got.PrimaryBlobDigest)
}

func TestDeleteArtifactDeletesDraftArtifact(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		DeleteArtifact(mock.Anything, catalog.DeleteArtifactParams{
			ImageName:  "debian",
			Version:    "v1.0.0",
			ArtifactID: catalogArtifactIDFixture(),
		}).
		Return(nil)

	req := newHTTPAPIRequest(
		http.MethodDelete,
		"/v1/images/debian/versions/v1.0.0/artifacts/"+catalogArtifactIDFixture().String(),
		nil,
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestAddAttachmentCreatesAttachmentUnderArtifactPath(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	wantAttachment := catalogAttachmentFixture()

	tc.catalog.EXPECT().
		AddAttachment(mock.Anything, catalog.AddAttachmentParams{
			ImageName:     "debian",
			Version:       "v1.0.0",
			ArtifactID:    catalogArtifactIDFixture(),
			Name:          "rootfs.sha256",
			MediaType:     "text/plain",
			BlobDigest:    catalogDigestFixture(),
			BlobSizeBytes: 64,
		}).
		Return(wantAttachment, nil)

	req := newHTTPAPIRequest(
		http.MethodPost,
		"/v1/images/debian/versions/v1.0.0/artifacts/"+catalogArtifactIDFixture().String()+"/attachments",
		strings.NewReader(`{
			"name": "rootfs.sha256",
			"media_type": "text/plain",
			"blob_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"blob_size_bytes": 64
		}`),
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var got attachmentResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, catalogAttachmentIDFixture().String(), got.ID)
	assert.Equal(t, catalogArtifactIDFixture().String(), got.ArtifactID)
	assert.Equal(t, "rootfs.sha256", got.Name)
}

func TestDeleteAttachmentDeletesDraftAttachment(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		DeleteAttachment(mock.Anything, catalog.DeleteAttachmentParams{
			ImageName:    "debian",
			Version:      "v1.0.0",
			ArtifactID:   catalogArtifactIDFixture(),
			AttachmentID: catalogAttachmentIDFixture(),
		}).
		Return(nil)

	req := newHTTPAPIRequest(
		http.MethodDelete,
		"/v1/images/debian/versions/v1.0.0/artifacts/"+
			catalogArtifactIDFixture().String()+
			"/attachments/"+
			catalogAttachmentIDFixture().String(),
		nil,
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestPublishVersionPublishesDraft(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	wantVersion := catalogVersionFixture(catalog.VersionStatePublished)

	tc.catalog.EXPECT().
		PublishVersion(mock.Anything, catalog.PublishVersionParams{
			ImageName: "debian",
			Version:   "v1.0.0",
		}).
		Return(wantVersion, nil)

	req := newHTTPAPIRequest(http.MethodPost, "/v1/images/debian/versions/v1.0.0/publish", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got versionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, catalog.VersionStatePublished, got.State)
	require.NotNil(t, got.PublishedAt)
	assert.Equal(t, catalogPublishedAtFixture().Format(time.RFC3339Nano), *got.PublishedAt)
}

func TestPutAliasCreatesOrMovesAlias(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)
	wantAlias := catalogAliasFixture()

	tc.catalog.EXPECT().
		PutAlias(mock.Anything, catalog.PutAliasParams{
			ImageName: "debian",
			Alias:     "latest",
			Version:   "v1.0.0",
		}).
		Return(wantAlias, nil)

	req := newHTTPAPIRequest(http.MethodPut, "/v1/images/debian/aliases/latest", strings.NewReader(`{
		"version": "v1.0.0"
	}`))
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got aliasResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, catalogAliasIDFixture().String(), got.ID)
	assert.Equal(t, catalogImageIDFixture().String(), got.ImageID)
	assert.Equal(t, "latest", got.Alias)
	assert.Equal(t, catalogVersionIDFixture().String(), got.VersionID)
	assert.Equal(t, "v1.0.0", got.Version)
}

func TestListAliasesReturnsAliases(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		ListAliases(mock.Anything, catalog.ListAliasesParams{ImageName: "debian"}).
		Return([]catalog.Alias{catalogAliasFixture()}, nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images/debian/aliases", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got aliasListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Aliases, 1)
	assert.Equal(t, "latest", got.Aliases[0].Alias)
	assert.Equal(t, "v1.0.0", got.Aliases[0].Version)
}

func TestGetAliasReturnsAlias(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		GetAlias(mock.Anything, catalog.GetAliasParams{
			ImageName: "debian",
			Alias:     "latest",
		}).
		Return(catalogAliasFixture(), nil)

	req := newHTTPAPIRequest(http.MethodGet, "/v1/images/debian/aliases/latest", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got aliasResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "latest", got.Alias)
	assert.Equal(t, "v1.0.0", got.Version)
}

func TestDeleteAliasDeletesAlias(t *testing.T) {
	tc := newCatalogHandlerTestContext(t)

	tc.catalog.EXPECT().
		DeleteAlias(mock.Anything, catalog.DeleteAliasParams{
			ImageName: "debian",
			Alias:     "latest",
		}).
		Return(nil)

	req := newHTTPAPIRequest(http.MethodDelete, "/v1/images/debian/aliases/latest", nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestCatalogHandlersRejectInvalidRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   string
	}{
		{
			name:   "create image rejects malformed JSON",
			method: http.MethodPost,
			path:   "/v1/images",
			body:   "{",
			want:   "invalid JSON request body",
		},
		{
			name:   "add artifact rejects invalid digest",
			method: http.MethodPost,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts",
			body:   `{"primary_blob_digest":"bad"}`,
			want:   "digest must match",
		},
		{
			name:   "put alias rejects malformed JSON",
			method: http.MethodPut,
			path:   "/v1/images/debian/aliases/latest",
			body:   "{",
			want:   "invalid JSON request body",
		},
		{
			name:   "add attachment rejects invalid artifact id",
			method: http.MethodPost,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts/not-a-uuid/attachments",
			body:   `{}`,
			want:   "artifact id must be a UUID",
		},
		{
			name:   "get artifact rejects invalid artifact id",
			method: http.MethodGet,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts/not-a-uuid",
			want:   "artifact id must be a UUID",
		},
		{
			name:   "download attachment rejects invalid attachment id",
			method: http.MethodGet,
			path: "/v1/images/debian/versions/v1.0.0/artifacts/" +
				catalogArtifactIDFixture().String() +
				"/attachments/not-a-uuid/download",
			want: "attachment id must be a UUID",
		},
		{
			name:   "delete artifact rejects invalid artifact id",
			method: http.MethodDelete,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts/not-a-uuid",
			want:   "artifact id must be a UUID",
		},
		{
			name:   "delete attachment rejects invalid attachment id",
			method: http.MethodDelete,
			path: "/v1/images/debian/versions/v1.0.0/artifacts/" +
				catalogArtifactIDFixture().String() +
				"/attachments/not-a-uuid",
			want: "attachment id must be a UUID",
		},
		{
			name:   "add attachment rejects invalid digest",
			method: http.MethodPost,
			path: "/v1/images/debian/versions/v1.0.0/artifacts/" +
				catalogArtifactIDFixture().String() +
				"/attachments",
			body: `{"blob_digest":"bad"}`,
			want: "digest must match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newCatalogHandlerTestContext(t)
			req := newHTTPAPIRequest(tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assertProblem(t, rec, http.StatusBadRequest, tt.want)
		})
	}
}

func TestCatalogHandlersMapDomainErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "invalid input", err: catalog.ErrInvalid, wantCode: http.StatusBadRequest},
		{name: "not found", err: catalog.ErrNotFound, wantCode: http.StatusNotFound},
		{name: "conflict", err: catalog.ErrConflict, wantCode: http.StatusConflict},
		{name: "failed precondition", err: catalog.ErrFailedPrecondition, wantCode: http.StatusPreconditionFailed},
		{name: "unexpected", err: errors.New("database unavailable"), wantCode: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newCatalogHandlerTestContext(t)
			tc.catalog.EXPECT().
				CreateImage(mock.Anything, mock.Anything).
				Return(catalog.Image{}, tt.err)
			req := newHTTPAPIRequest(http.MethodPost, "/v1/images", strings.NewReader(`{"name":"debian"}`))
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			require.Equal(t, tt.wantCode, rec.Code)
			assertProblem(t, rec, tt.wantCode, tt.err.Error())
		})
	}
}

func TestCatalogHandlersReturnUnavailableWhenServiceMissing(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "create image", method: http.MethodPost, path: "/v1/images", body: `{}`},
		{name: "list images", method: http.MethodGet, path: "/v1/images"},
		{name: "get image", method: http.MethodGet, path: "/v1/images/debian"},
		{name: "create version", method: http.MethodPost, path: "/v1/images/debian/versions", body: `{}`},
		{name: "list versions", method: http.MethodGet, path: "/v1/images/debian/versions"},
		{name: "get manifest", method: http.MethodGet, path: "/v1/images/debian/versions/v1.0.0"},
		{name: "list published artifacts", method: http.MethodGet, path: "/v1/images/debian/versions/v1.0.0/artifacts"},
		{
			name:   "get published artifact",
			method: http.MethodGet,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts/" + catalogArtifactIDFixture().String(),
		},
		{
			name:   "download published artifact",
			method: http.MethodGet,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts/" + catalogArtifactIDFixture().String() + "/download",
		},
		{name: "resolve manifest", method: http.MethodGet, path: "/v1/images/debian/refs/latest"},
		{
			name:   "add artifact",
			method: http.MethodPost,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts",
			body:   `{}`,
		},
		{
			name:   "add attachment",
			method: http.MethodPost,
			path: "/v1/images/debian/versions/v1.0.0/artifacts/" + catalogArtifactIDFixture().String() +
				"/attachments",
			body: `{}`,
		},
		{
			name:   "delete artifact",
			method: http.MethodDelete,
			path:   "/v1/images/debian/versions/v1.0.0/artifacts/" + catalogArtifactIDFixture().String(),
		},
		{
			name:   "delete attachment",
			method: http.MethodDelete,
			path: "/v1/images/debian/versions/v1.0.0/artifacts/" +
				catalogArtifactIDFixture().String() +
				"/attachments/" +
				catalogAttachmentIDFixture().String(),
		},
		{name: "publish", method: http.MethodPost, path: "/v1/images/debian/versions/v1.0.0/publish"},
		{name: "put alias", method: http.MethodPut, path: "/v1/images/debian/aliases/latest", body: `{}`},
		{name: "list aliases", method: http.MethodGet, path: "/v1/images/debian/aliases"},
		{name: "get alias", method: http.MethodGet, path: "/v1/images/debian/aliases/latest"},
		{name: "delete alias", method: http.MethodDelete, path: "/v1/images/debian/aliases/latest"},
		{
			name:   "download published attachment",
			method: http.MethodGet,
			path: "/v1/images/debian/versions/v1.0.0/artifacts/" +
				catalogArtifactIDFixture().String() +
				"/attachments/" +
				catalogAttachmentIDFixture().String() +
				"/download",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := New(Dependencies{
				Auth: newAcceptingAuthService(t),
			})
			req := newHTTPAPIRequest(tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			assertProblem(t, rec, http.StatusServiceUnavailable, errCatalogServiceUnavailable.Error())
		})
	}
}

type catalogHandlerTestContext struct {
	catalog *httpmocks.MockCatalogService
	blobs   *httpmocks.MockBlobService
	handler http.Handler
}

func newCatalogHandlerTestContext(t *testing.T) *catalogHandlerTestContext {
	t.Helper()

	catalogService := httpmocks.NewMockCatalogService(t)
	blobService := httpmocks.NewMockBlobService(t)
	return &catalogHandlerTestContext{
		catalog: catalogService,
		blobs:   blobService,
		handler: New(Dependencies{
			Catalog: catalogService,
			Blobs:   blobService,
			Auth:    newAcceptingAuthService(t),
		}),
	}
}

func catalogImageFixture() catalog.Image {
	return catalog.Image{
		ID:        catalogImageIDFixture(),
		Name:      "debian",
		CreatedAt: catalogCreatedAtFixture(),
		UpdatedAt: catalogUpdatedAtFixture(),
	}
}

func catalogVersionFixture(state catalog.VersionState) catalog.Version {
	version := catalog.Version{
		ID:        catalogVersionIDFixture(),
		ImageID:   catalogImageIDFixture(),
		Version:   "v1.0.0",
		State:     state,
		CreatedAt: catalogCreatedAtFixture(),
		UpdatedAt: catalogUpdatedAtFixture(),
	}
	if state == catalog.VersionStatePublished {
		publishedAt := catalogPublishedAtFixture()
		version.PublishedAt = &publishedAt
	}

	return version
}

func catalogArtifactFixture() catalog.Artifact {
	return catalog.Artifact{
		ID:                   catalogArtifactIDFixture(),
		VersionID:            catalogVersionIDFixture(),
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               catalog.ArtifactFormatRawGZ,
		PrimaryBlobDigest:    catalogDigestFixture(),
		PrimaryBlobSizeBytes: 1024,
		PrimaryMediaType:     "application/gzip",
		CreatedAt:            catalogCreatedAtFixture(),
		UpdatedAt:            catalogUpdatedAtFixture(),
	}
}

func catalogAttachmentFixture() catalog.Attachment {
	return catalog.Attachment{
		ID:            catalogAttachmentIDFixture(),
		ArtifactID:    catalogArtifactIDFixture(),
		Name:          "rootfs.sha256",
		MediaType:     "text/plain",
		BlobDigest:    catalogDigestFixture(),
		BlobSizeBytes: 64,
		CreatedAt:     catalogCreatedAtFixture(),
		UpdatedAt:     catalogUpdatedAtFixture(),
	}
}

func catalogAliasFixture() catalog.Alias {
	return catalog.Alias{
		ID:        catalogAliasIDFixture(),
		ImageID:   catalogImageIDFixture(),
		Alias:     "latest",
		VersionID: catalogVersionIDFixture(),
		Version:   "v1.0.0",
		CreatedAt: catalogCreatedAtFixture(),
		UpdatedAt: catalogUpdatedAtFixture(),
	}
}

func catalogManifestFixture(state catalog.VersionState) catalog.Manifest {
	return catalog.Manifest{
		Image:   catalogImageFixture(),
		Version: catalogVersionFixture(state),
		Artifacts: []catalog.ManifestArtifact{{
			Artifact:    catalogArtifactFixture(),
			Attachments: []catalog.Attachment{catalogAttachmentFixture()},
		}},
	}
}

func catalogImageIDFixture() uuid.UUID {
	return uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
}

func catalogVersionIDFixture() uuid.UUID {
	return uuid.MustParse("bbbbbbbb-cccc-dddd-eeee-ffffffffffff")
}

func catalogArtifactIDFixture() uuid.UUID {
	return uuid.MustParse("cccccccc-dddd-eeee-ffff-000000000000")
}

func catalogAttachmentIDFixture() uuid.UUID {
	return uuid.MustParse("dddddddd-eeee-ffff-0000-111111111111")
}

func catalogAliasIDFixture() uuid.UUID {
	return uuid.MustParse("eeeeeeee-ffff-0000-1111-222222222222")
}

func catalogDigestFixture() catalog.Digest {
	return catalog.Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
}

func catalogCreatedAtFixture() time.Time {
	return time.Date(2026, 5, 5, 18, 0, 0, 0, time.UTC)
}

func catalogUpdatedAtFixture() time.Time {
	return time.Date(2026, 5, 5, 18, 1, 0, 0, time.UTC)
}

func catalogPublishedAtFixture() time.Time {
	return time.Date(2026, 5, 5, 18, 2, 0, 0, time.UTC)
}

func rangePtr(start int64, end int64) *objectstore.ByteRange {
	return &objectstore.ByteRange{Start: start, End: end}
}
