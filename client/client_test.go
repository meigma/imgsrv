package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testArtifactID   = "33333333-4444-5555-6666-777777777777"
	testAttachmentID = "44444444-5555-6666-7777-888888888888"
	testAliasID      = "66666666-7777-8888-9999-aaaaaaaaaaaa"
	testDigest       = Digest(
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	testImageID   = "22222222-3333-4444-5555-666666666666"
	testUploadID  = "11111111-2222-3333-4444-555555555555"
	testVersionID = "55555555-6666-7777-8888-999999999999"
)

func TestNewValidatesBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr string
	}{
		{
			name:    "missing",
			wantErr: "base url is required",
		},
		{
			name:    "unsupported scheme",
			baseURL: "ftp://imgsrv.example.test",
			wantErr: "http or https",
		},
		{
			name:    "missing host",
			baseURL: "https:///v1",
			wantErr: "host",
		},
		{
			name:    "query",
			baseURL: "https://imgsrv.example.test?debug=true",
			wantErr: "query or fragment",
		},
		{
			name:    "valid",
			baseURL: "https://imgsrv.example.test/api/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(Options{BaseURL: tt.baseURL})

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestClientUploadFlowBuildsRequests(t *testing.T) {
	ctx := context.Background()
	mediaType := "application/octet-stream"
	filename := "image.qcow2"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/uploads", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var got BeginUploadRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
			return
		}
		assert.Equal(t, testDigest, got.ExpectedDigest)
		assert.Equal(t, int64(12), got.ExpectedSizeBytes)
		if assert.NotNil(t, got.MediaTypeHint) {
			assert.Equal(t, mediaType, *got.MediaTypeHint)
		}
		if assert.NotNil(t, got.FilenameHint) {
			assert.Equal(t, filename, *got.FilenameHint)
		}

		writeJSON(t, w, http.StatusCreated, uploadSessionFixture(UploadStateCreated))
	})
	mux.HandleFunc(
		"/api/v1/uploads/"+testUploadID+"/parts/1",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, http.MethodPut)
			assert.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
			assert.Equal(t, int64(12), r.ContentLength)
			body, err := io.ReadAll(r.Body)
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, "hello upload", string(body))

			writeJSON(t, w, http.StatusOK, UploadPart{
				UploadID:   UploadID(testUploadID),
				PartNumber: 1,
				ETag:       "etag-1",
				SizeBytes:  12,
			})
		},
	)
	mux.HandleFunc(
		"/api/v1/uploads/"+testUploadID+"/complete",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, http.MethodPost)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var got CompleteUploadRequest
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
				return
			}
			if !assert.Len(t, got.Parts, 1) {
				return
			}
			assert.Equal(
				t,
				CompleteUploadPart{Number: 1, ETag: "etag-1", SizeBytes: 12},
				got.Parts[0],
			)

			writeJSON(t, w, http.StatusOK, uploadSessionFixture(UploadStateCompleted))
		},
	)
	mux.HandleFunc(
		"/api/v1/uploads/"+testUploadID+"/abort",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, http.MethodPost)
			assert.Zero(t, r.ContentLength)

			writeJSON(t, w, http.StatusOK, uploadSessionFixture(UploadStateAborted))
		},
	)
	mux.HandleFunc("/api/v1/uploads/"+testUploadID, func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodGet)
		writeJSON(t, w, http.StatusOK, uploadSessionFixture(UploadStateCompleted))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := New(Options{
		BaseURL:     server.URL + "/api/",
		BearerToken: "test-token",
		UserAgent:   "imgsrv-test-client",
	})
	require.NoError(t, err)
	uploads := client.Uploads()
	require.NotNil(t, uploads)

	begin, err := uploads.BeginUpload(ctx, BeginUploadRequest{
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
		MediaTypeHint:     &mediaType,
		FilenameHint:      &filename,
	})
	require.NoError(t, err)
	assert.Equal(t, UploadID(testUploadID), begin.ID)
	assert.Equal(t, UploadStateCreated, begin.State)

	part, err := uploads.PutUploadPart(
		ctx,
		begin.ID.String(),
		1,
		strings.NewReader("hello upload"),
		12,
	)
	require.NoError(t, err)
	assert.Equal(t, "etag-1", part.ETag)

	complete, err := uploads.CompleteUpload(ctx, begin.ID.String(), CompleteUploadRequest{
		Parts: []CompleteUploadPart{
			{Number: part.PartNumber, ETag: part.ETag, SizeBytes: part.SizeBytes},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, UploadStateCompleted, complete.State)

	aborted, err := uploads.AbortUpload(ctx, begin.ID.String())
	require.NoError(t, err)
	assert.Equal(t, UploadStateAborted, aborted.State)

	status, err := uploads.GetUpload(ctx, begin.ID.String())
	require.NoError(t, err)
	assert.Equal(t, UploadStateCompleted, status.State)
}

func TestClientBeginUploadAcceptsReadyShortCircuitStatus(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/uploads", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		writeJSON(t, w, http.StatusOK, uploadSessionFixture(UploadStateReady))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := New(Options{
		BaseURL:     server.URL + "/api/",
		BearerToken: "test-token",
		UserAgent:   "imgsrv-test-client",
	})
	require.NoError(t, err)

	session, err := client.Uploads().BeginUpload(ctx, BeginUploadRequest{
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
	})
	require.NoError(t, err)
	assert.Equal(t, UploadStateReady, session.State)
}

func TestClientCatalogFlowBuildsRequests(t *testing.T) {
	ctx := context.Background()
	displayName := "Debian 12"
	description := "Base image"
	server := newCatalogFlowServer(t, displayName, description)
	t.Cleanup(server.Close)

	client, err := New(Options{
		BaseURL:     server.URL + "/api/",
		BearerToken: "test-token",
		UserAgent:   "imgsrv-test-client",
	})
	require.NoError(t, err)
	catalog := client.Catalog()
	require.NotNil(t, catalog)

	image, err := catalog.CreateImage(ctx, CreateImageRequest{
		Name:        "debian",
		DisplayName: &displayName,
		Description: &description,
	})
	require.NoError(t, err)
	assert.Equal(t, testImageID, image.ID)

	images, err := catalog.ListImages(ctx)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, image.ID, images[0].ID)

	gotImage, err := catalog.GetImage(ctx, image.Name)
	require.NoError(t, err)
	assert.Equal(t, image.ID, gotImage.ID)

	version, err := catalog.CreateDraftVersion(
		ctx,
		image.Name,
		CreateDraftVersionRequest{Version: "v1.0.0"},
	)
	require.NoError(t, err)
	assert.Equal(t, ImageVersionStateDraft, version.State)

	versions, err := catalog.ListVersions(ctx, image.Name)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, version.ID, versions[0].ID)

	artifact, err := catalog.AddArtifact(ctx, image.Name, version.Version, AddArtifactRequest{
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               ArtifactFormatRawGZ,
		PrimaryBlobDigest:    testDigest,
		PrimaryBlobSizeBytes: 12,
		PrimaryMediaType:     "application/gzip",
	})
	require.NoError(t, err)
	assert.Equal(t, ArtifactID(testArtifactID), artifact.ID)

	attachment, err := catalog.AddAttachment(
		ctx,
		image.Name,
		version.Version,
		artifact.ID.String(),
		AddAttachmentRequest{
			Name:          "rootfs.sha256",
			MediaType:     "text/plain",
			BlobDigest:    testDigest,
			BlobSizeBytes: 64,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, testAttachmentID, attachment.ID)

	require.NoError(
		t,
		catalog.DeleteAttachment(
			ctx,
			image.Name,
			version.Version,
			artifact.ID.String(),
			attachment.ID,
		),
	)
	require.NoError(
		t,
		catalog.DeleteArtifact(ctx, image.Name, version.Version, artifact.ID.String()),
	)

	manifest, err := catalog.GetVersionManifest(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assert.Equal(t, ImageVersionStateDraft, manifest.Version.State)
	require.Len(t, manifest.Artifacts, 1)

	published, err := catalog.PublishVersion(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assert.Equal(t, ImageVersionStatePublished, published.State)

	alias, err := catalog.PutAlias(
		ctx,
		image.Name,
		"latest",
		PutAliasRequest{Version: version.Version},
	)
	require.NoError(t, err)
	assert.Equal(t, "latest", alias.Alias)
	assert.Equal(t, version.Version, alias.Version)

	aliases, err := catalog.ListAliases(ctx, image.Name)
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, alias.ID, aliases[0].ID)

	gotAlias, err := catalog.GetAlias(ctx, image.Name, alias.Alias)
	require.NoError(t, err)
	assert.Equal(t, alias.ID, gotAlias.ID)

	resolved, err := catalog.ResolveManifest(ctx, image.Name, alias.Alias)
	require.NoError(t, err)
	assert.Equal(t, ImageVersionStatePublished, resolved.Version.State)

	artifacts, err := catalog.ListArtifacts(ctx, image.Name, version.Version)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	assert.Equal(t, artifact.ID, artifacts[0].ID)

	gotArtifact, err := catalog.GetArtifact(ctx, image.Name, version.Version, artifact.ID.String())
	require.NoError(t, err)
	assert.Equal(t, artifact.ID, gotArtifact.ID)

	artifactDownload, err := catalog.OpenArtifactDownload(
		ctx,
		image.Name,
		version.Version,
		artifact.ID.String(),
		OpenBlobOptions{},
	)
	require.NoError(t, err)
	artifactBody, err := io.ReadAll(artifactDownload.Body)
	require.NoError(t, err)
	require.NoError(t, artifactDownload.Body.Close())
	assert.Equal(t, "artifact-bytes", string(artifactBody))
	assert.Equal(t, testDigest, artifactDownload.Metadata.Digest)
	assert.Equal(t, "application/gzip", artifactDownload.Metadata.ContentType)

	downloadRange, err := BlobRangeSpan(1, 3)
	require.NoError(t, err)
	attachmentDownload, err := catalog.OpenAttachmentDownload(
		ctx,
		image.Name,
		version.Version,
		artifact.ID.String(),
		testAttachmentID,
		OpenBlobOptions{Range: &downloadRange},
	)
	require.NoError(t, err)
	attachmentBody, err := io.ReadAll(attachmentDownload.Body)
	require.NoError(t, err)
	require.NoError(t, attachmentDownload.Body.Close())
	assert.Equal(t, "oot", string(attachmentBody))
	assert.Equal(t, "bytes 1-3/8", attachmentDownload.Metadata.ContentRange)

	require.NoError(t, catalog.DeleteAlias(ctx, image.Name, alias.Alias))
}

func newCatalogFlowServer(t *testing.T, displayName string, description string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	registerCatalogImageHandlers(t, mux, displayName, description)
	registerCatalogVersionHandlers(t, mux)
	registerCatalogArtifactHandlers(t, mux)
	registerCatalogAliasHandlers(t, mux)

	return httptest.NewServer(mux)
}

func registerCatalogImageHandlers(
	t *testing.T,
	mux *http.ServeMux,
	displayName string,
	description string,
) {
	t.Helper()

	mux.HandleFunc("/api/v1/images", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, r.Method)

		switch r.Method {
		case http.MethodPost:
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var got CreateImageRequest
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
				return
			}
			assert.Equal(t, "debian", got.Name)
			if assert.NotNil(t, got.DisplayName) {
				assert.Equal(t, displayName, *got.DisplayName)
			}
			if assert.NotNil(t, got.Description) {
				assert.Equal(t, description, *got.Description)
			}
			writeJSON(t, w, http.StatusCreated, imageFixture())
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, imageListResponse{Images: []Image{imageFixture()}})
		default:
			t.Fatalf("unexpected images method %s", r.Method)
		}
	})
	mux.HandleFunc("/api/v1/images/debian", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodGet)
		writeJSON(t, w, http.StatusOK, imageFixture())
	})
}

func registerCatalogVersionHandlers(t *testing.T, mux *http.ServeMux) {
	t.Helper()

	mux.HandleFunc("/api/v1/images/debian/versions", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, r.Method)

		switch r.Method {
		case http.MethodPost:
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var got CreateDraftVersionRequest
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
				return
			}
			assert.Equal(t, "v1.0.0", got.Version)
			writeJSON(t, w, http.StatusCreated, versionFixture(ImageVersionStateDraft))
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, versionListResponse{
				Versions: []ImageVersion{versionFixture(ImageVersionStateDraft)},
			})
		default:
			t.Fatalf("unexpected versions method %s", r.Method)
		}
	})
	mux.HandleFunc(
		"/api/v1/images/debian/versions/v1.0.0",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, http.MethodGet)
			writeJSON(t, w, http.StatusOK, manifestFixture(ImageVersionStateDraft))
		},
	)
	mux.HandleFunc(
		"/api/v1/images/debian/versions/v1.0.0/publish",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, http.MethodPost)
			assert.Zero(t, r.ContentLength)
			writeJSON(t, w, http.StatusOK, versionFixture(ImageVersionStatePublished))
		},
	)
	mux.HandleFunc(
		"/api/v1/images/debian/refs/latest",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, http.MethodGet)
			writeJSON(t, w, http.StatusOK, manifestFixture(ImageVersionStatePublished))
		},
	)
}

func registerCatalogArtifactHandlers(t *testing.T, mux *http.ServeMux) {
	t.Helper()

	artifactPath := "/api/v1/images/debian/versions/v1.0.0/artifacts/" + testArtifactID
	mux.HandleFunc(
		"/api/v1/images/debian/versions/v1.0.0/artifacts",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, r.Method)

			switch r.Method {
			case http.MethodPost:
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				var got AddArtifactRequest
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
					return
				}
				assert.Equal(t, "linux", got.OperatingSystem)
				assert.Equal(t, "x86_64", got.Architecture)
				assert.Equal(t, ArtifactFormatRawGZ, got.Format)
				assert.Equal(t, testDigest, got.PrimaryBlobDigest)
				assert.Equal(t, int64(12), got.PrimaryBlobSizeBytes)
				assert.Equal(t, "application/gzip", got.PrimaryMediaType)
				writeJSON(t, w, http.StatusCreated, artifactFixture())
			case http.MethodGet:
				writeJSON(
					t,
					w,
					http.StatusOK,
					artifactListResponse{Artifacts: []Artifact{artifactFixture()}},
				)
			default:
				t.Fatalf("unexpected artifacts method %s", r.Method)
			}
		},
	)
	mux.HandleFunc(artifactPath, func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, r.Method)

		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, artifactFixture())
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected artifact method %s", r.Method)
		}
	})
	mux.HandleFunc(artifactPath+"/download", func(w http.ResponseWriter, r *http.Request) {
		assertBlobDownloadRequest(t, r, "")
		writeDownload(t, w, http.StatusOK, "application/gzip", "14", "", "artifact-bytes")
	})
	mux.HandleFunc(artifactPath+"/attachments", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var got AddAttachmentRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
			return
		}
		assert.Equal(t, "rootfs.sha256", got.Name)
		assert.Equal(t, "text/plain", got.MediaType)
		assert.Equal(t, testDigest, got.BlobDigest)
		assert.Equal(t, int64(64), got.BlobSizeBytes)

		writeJSON(t, w, http.StatusCreated, attachmentFixture())
	})
	mux.HandleFunc(
		artifactPath+"/attachments/"+testAttachmentID,
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, http.MethodDelete)
			w.WriteHeader(http.StatusNoContent)
		},
	)
	mux.HandleFunc(
		artifactPath+"/attachments/"+testAttachmentID+"/download",
		func(w http.ResponseWriter, r *http.Request) {
			assertBlobDownloadRequest(t, r, "bytes=1-3")
			writeDownload(t, w, http.StatusPartialContent, "text/plain", "3", "bytes 1-3/8", "oot")
		},
	)
}

func registerCatalogAliasHandlers(t *testing.T, mux *http.ServeMux) {
	t.Helper()

	mux.HandleFunc("/api/v1/images/debian/aliases", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodGet)
		writeJSON(t, w, http.StatusOK, aliasListResponse{Aliases: []Alias{aliasFixture()}})
	})
	mux.HandleFunc(
		"/api/v1/images/debian/aliases/latest",
		func(w http.ResponseWriter, r *http.Request) {
			assertRequestBasics(t, r, r.Method)

			switch r.Method {
			case http.MethodPut:
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				var got PutAliasRequest
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
					return
				}
				assert.Equal(t, "v1.0.0", got.Version)
				writeJSON(t, w, http.StatusOK, aliasFixture())
			case http.MethodGet:
				writeJSON(t, w, http.StatusOK, aliasFixture())
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected alias method %s", r.Method)
			}
		},
	)
}

func TestClientBlobFlowBuildsRequests(t *testing.T) {
	ctx := context.Background()
	rangeFrom, err := BlobRangeFrom(3)
	require.NoError(t, err)
	suffixRange, err := BlobRangeSuffix(4)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc(
		"/api/v1/blobs/"+url.PathEscape(testDigest.String()),
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "*/*", r.Header.Get("Accept"))
			assert.Equal(t, "imgsrv-test-client", r.Header.Get("User-Agent"))
			assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

			switch {
			case r.Method == http.MethodHead:
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", "10")
				w.Header().
					Set("ETag", `"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`)
				w.Header().Set("Last-Modified", "Mon, 05 May 2026 12:00:00 GMT")
				w.WriteHeader(http.StatusOK)
			case r.Method == http.MethodGet && r.Header.Get("Range") == "":
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", "10")
				w.Header().
					Set("ETag", `"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`)
				w.Header().Set("Last-Modified", "Mon, 05 May 2026 12:00:00 GMT")
				_, writeErr := w.Write([]byte("0123456789"))
				assert.NoError(t, writeErr)
			case r.Method == http.MethodGet && r.Header.Get("Range") == "bytes=3-":
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", "7")
				w.Header().Set("Content-Range", "bytes 3-9/10")
				w.Header().
					Set("ETag", `"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`)
				w.Header().Set("Last-Modified", "Mon, 05 May 2026 12:00:00 GMT")
				w.WriteHeader(http.StatusPartialContent)
				_, writeErr := w.Write([]byte("3456789"))
				assert.NoError(t, writeErr)
			case r.Method == http.MethodGet && r.Header.Get("Range") == "bytes=-4":
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", "4")
				w.Header().Set("Content-Range", "bytes 6-9/10")
				w.Header().
					Set("ETag", `"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`)
				w.Header().Set("Last-Modified", "Mon, 05 May 2026 12:00:00 GMT")
				w.WriteHeader(http.StatusPartialContent)
				_, writeErr := w.Write([]byte("6789"))
				assert.NoError(t, writeErr)
			default:
				t.Fatalf(
					"unexpected blob request: method=%s range=%q",
					r.Method,
					r.Header.Get("Range"),
				)
			}
		},
	)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := New(Options{
		BaseURL:     server.URL + "/api/",
		BearerToken: "test-token",
		UserAgent:   "imgsrv-test-client",
	})
	require.NoError(t, err)
	blobs := client.Blobs()
	require.NotNil(t, blobs)

	head, err := blobs.HeadBlob(ctx, testDigest)
	require.NoError(t, err)
	assert.Equal(t, testDigest, head.Digest)
	assert.Equal(t, int64(10), head.SizeBytes)
	assert.Equal(t, int64(10), head.ContentLength)
	assert.Equal(t, "application/octet-stream", head.ContentType)
	assert.Equal(t, "bytes", head.AcceptRanges)

	full, err := blobs.OpenBlob(ctx, testDigest, OpenBlobOptions{})
	require.NoError(t, err)
	fullBody, err := io.ReadAll(full.Body)
	require.NoError(t, err)
	require.NoError(t, full.Body.Close())
	assert.Equal(t, "0123456789", string(fullBody))
	assert.Equal(t, int64(10), full.Metadata.SizeBytes)
	assert.Equal(t, int64(10), full.Metadata.ContentLength)
	assert.Empty(t, full.Metadata.ContentRange)

	ranged, err := blobs.OpenBlob(ctx, testDigest, OpenBlobOptions{Range: &rangeFrom})
	require.NoError(t, err)
	rangedBody, err := io.ReadAll(ranged.Body)
	require.NoError(t, err)
	require.NoError(t, ranged.Body.Close())
	assert.Equal(t, "3456789", string(rangedBody))
	assert.Equal(t, int64(10), ranged.Metadata.SizeBytes)
	assert.Equal(t, int64(7), ranged.Metadata.ContentLength)
	assert.Equal(t, "bytes 3-9/10", ranged.Metadata.ContentRange)

	suffix, err := blobs.OpenBlob(ctx, testDigest, OpenBlobOptions{Range: &suffixRange})
	require.NoError(t, err)
	suffixBody, err := io.ReadAll(suffix.Body)
	require.NoError(t, err)
	require.NoError(t, suffix.Body.Close())
	assert.Equal(t, "6789", string(suffixBody))
	assert.Equal(t, "bytes 6-9/10", suffix.Metadata.ContentRange)
}

func TestClientDecodesProblemError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, err := w.Write([]byte(`{
			"type": "about:blank",
			"title": "Precondition Failed",
			"status": 412,
			"detail": "upload session cannot be completed",
			"instance": "/problems/123",
			"retry_after_seconds": 30
		}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	client, err := New(Options{BaseURL: server.URL})
	require.NoError(t, err)

	_, err = client.Uploads().GetUpload(context.Background(), testUploadID)

	var problem *ProblemError
	require.ErrorAs(t, err, &problem)
	assert.Equal(t, http.StatusPreconditionFailed, problem.HTTPStatus)
	assert.Equal(t, "about:blank", problem.Type)
	assert.Equal(t, "Precondition Failed", problem.Title)
	assert.Equal(t, http.StatusPreconditionFailed, problem.Status)
	assert.Equal(t, "upload session cannot be completed", problem.Detail)
	assert.Equal(t, "/problems/123", problem.Instance)
	assert.JSONEq(t, "30", string(problem.Extensions["retry_after_seconds"]))
	assert.Contains(t, problem.Error(), "Precondition Failed")
}

func TestClientDefaultsMissingProblemTypeToAboutBlank(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, err := w.Write([]byte(`{"title":"Not Found","status":404}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	client, err := New(Options{BaseURL: server.URL})
	require.NoError(t, err)

	_, err = client.Uploads().GetUpload(context.Background(), testUploadID)

	var problem *ProblemError
	require.ErrorAs(t, err, &problem)
	assert.Equal(t, "about:blank", problem.Type)
}

func TestClientReturnsHTTPErrorForNonProblemFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client, err := New(Options{BaseURL: server.URL})
	require.NoError(t, err)

	_, err = client.Uploads().GetUpload(context.Background(), testUploadID)

	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
	assert.Contains(t, string(httpErr.Body), "database unavailable")
}

func TestProblemErrorIgnoresMalformedKnownMembers(t *testing.T) {
	var problem ProblemError

	err := json.Unmarshal([]byte(`{"type": 42, "status": "bad", "custom_value": true}`), &problem)

	require.NoError(t, err)
	assert.Empty(t, problem.Type)
	assert.Zero(t, problem.Status)
	assert.JSONEq(t, "true", string(problem.Extensions["custom_value"]))
}

func assertRequestBasics(t *testing.T, r *http.Request, method string) {
	t.Helper()

	assert.Equal(t, method, r.Method)
	assert.Equal(t, "application/json, application/problem+json", r.Header.Get("Accept"))
	assert.Equal(t, "imgsrv-test-client", r.Header.Get("User-Agent"))
	assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
}

func assertBlobDownloadRequest(t *testing.T, r *http.Request, rangeValue string) {
	t.Helper()

	assert.Equal(t, http.MethodGet, r.Method)
	assert.Equal(t, "*/*", r.Header.Get("Accept"))
	assert.Equal(t, rangeValue, r.Header.Get("Range"))
	assert.Equal(t, "imgsrv-test-client", r.Header.Get("User-Agent"))
	assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
}

func writeDownload(
	t *testing.T,
	w http.ResponseWriter,
	status int,
	contentType string,
	contentLength string,
	contentRange string,
	body string,
) {
	t.Helper()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", contentLength)
	w.Header().
		Set("ETag", `"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`)
	w.Header().Set("Last-Modified", "Mon, 05 May 2026 12:00:00 GMT")
	if contentRange != "" {
		w.Header().Set("Content-Range", contentRange)
	}
	w.WriteHeader(status)
	_, err := w.Write([]byte(body))
	assert.NoError(t, err)
}

func uploadSessionFixture(state UploadState) UploadSession {
	return UploadSession{
		ID:                UploadID(testUploadID),
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
		State:             state,
		ExpiresAt:         "2026-05-05T12:00:00Z",
	}
}

func imageFixture() Image {
	displayName := "Debian 12"
	description := "Base image"

	return Image{
		ID:          testImageID,
		Name:        "debian",
		DisplayName: &displayName,
		Description: &description,
		CreatedAt:   "2026-05-05T12:00:00Z",
		UpdatedAt:   "2026-05-05T12:00:00Z",
	}
}

func versionFixture(state ImageVersionState) ImageVersion {
	version := ImageVersion{
		ID:        testVersionID,
		ImageID:   testImageID,
		Version:   "v1.0.0",
		State:     state,
		CreatedAt: "2026-05-05T12:00:00Z",
		UpdatedAt: "2026-05-05T12:00:00Z",
	}
	if state == ImageVersionStatePublished {
		publishedAt := "2026-05-05T12:01:00Z"
		version.PublishedAt = &publishedAt
	}

	return version
}

func artifactFixture() Artifact {
	return Artifact{
		ID:                   ArtifactID(testArtifactID),
		VersionID:            testVersionID,
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               ArtifactFormatRawGZ,
		PrimaryBlobDigest:    testDigest,
		PrimaryBlobSizeBytes: 12,
		PrimaryMediaType:     "application/gzip",
		CreatedAt:            "2026-05-05T12:00:00Z",
		UpdatedAt:            "2026-05-05T12:00:00Z",
	}
}

func attachmentFixture() Attachment {
	return Attachment{
		ID:            testAttachmentID,
		ArtifactID:    ArtifactID(testArtifactID),
		Name:          "rootfs.sha256",
		MediaType:     "text/plain",
		BlobDigest:    testDigest,
		BlobSizeBytes: 64,
		CreatedAt:     "2026-05-05T12:00:00Z",
		UpdatedAt:     "2026-05-05T12:00:00Z",
	}
}

func aliasFixture() Alias {
	return Alias{
		ID:        testAliasID,
		ImageID:   testImageID,
		Alias:     "latest",
		VersionID: testVersionID,
		Version:   "v1.0.0",
		CreatedAt: "2026-05-05T12:00:00Z",
		UpdatedAt: "2026-05-05T12:00:00Z",
	}
}

func manifestFixture(state ImageVersionState) Manifest {
	return Manifest{
		Image:   imageFixture(),
		Version: versionFixture(state),
		Artifacts: []ManifestArtifact{{
			Artifact:    artifactFixture(),
			Attachments: []Attachment{attachmentFixture()},
		}},
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
