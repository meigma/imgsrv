package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testArtifactID   = "33333333-4444-5555-6666-777777777777"
	testAttachmentID = "44444444-5555-6666-7777-888888888888"
	testDigest       = Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	testImageID      = "22222222-3333-4444-5555-666666666666"
	testUploadID     = "11111111-2222-3333-4444-555555555555"
	testVersionID    = "55555555-6666-7777-8888-999999999999"
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
	mux.HandleFunc("/api/v1/uploads/"+testUploadID+"/parts/1", func(w http.ResponseWriter, r *http.Request) {
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
	})
	mux.HandleFunc("/api/v1/uploads/"+testUploadID+"/complete", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var got CompleteUploadRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
			return
		}
		if !assert.Len(t, got.Parts, 1) {
			return
		}
		assert.Equal(t, CompleteUploadPart{Number: 1, ETag: "etag-1", SizeBytes: 12}, got.Parts[0])

		writeJSON(t, w, http.StatusOK, uploadSessionFixture(UploadStateCompleted))
	})
	mux.HandleFunc("/api/v1/uploads/"+testUploadID+"/abort", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		assert.Zero(t, r.ContentLength)

		writeJSON(t, w, http.StatusOK, uploadSessionFixture(UploadStateAborted))
	})
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

	part, err := uploads.PutUploadPart(ctx, begin.ID.String(), 1, strings.NewReader("hello upload"), 12)
	require.NoError(t, err)
	assert.Equal(t, "etag-1", part.ETag)

	complete, err := uploads.CompleteUpload(ctx, begin.ID.String(), CompleteUploadRequest{
		Parts: []CompleteUploadPart{{Number: part.PartNumber, ETag: part.ETag, SizeBytes: part.SizeBytes}},
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

func TestClientCatalogFlowBuildsRequests(t *testing.T) {
	ctx := context.Background()
	displayName := "Debian 12"
	description := "Base image"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/images", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
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
	})
	mux.HandleFunc("/api/v1/images/debian/versions", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var got CreateDraftVersionRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
			return
		}
		assert.Equal(t, "v1.0.0", got.Version)

		writeJSON(t, w, http.StatusCreated, versionFixture(ImageVersionStateDraft))
	})
	mux.HandleFunc("/api/v1/images/debian/versions/v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodGet)
		writeJSON(t, w, http.StatusOK, manifestFixture(ImageVersionStateDraft))
	})
	mux.HandleFunc("/api/v1/images/debian/versions/v1.0.0/artifacts", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var got AddArtifactRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
			return
		}
		assert.Equal(t, "linux", got.OperatingSystem)
		assert.Equal(t, "x86_64", got.Architecture)
		assert.Equal(t, ArtifactFormatQCOW2, got.Format)
		assert.Equal(t, testDigest, got.PrimaryBlobDigest)
		assert.Equal(t, int64(12), got.PrimaryBlobSizeBytes)
		assert.Equal(t, "application/x-qcow2", got.PrimaryMediaType)

		writeJSON(t, w, http.StatusCreated, artifactFixture())
	})
	mux.HandleFunc(
		"/api/v1/images/debian/versions/v1.0.0/artifacts/"+testArtifactID+"/attachments",
		func(w http.ResponseWriter, r *http.Request) {
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
		},
	)
	mux.HandleFunc("/api/v1/images/debian/versions/v1.0.0/publish", func(w http.ResponseWriter, r *http.Request) {
		assertRequestBasics(t, r, http.MethodPost)
		assert.Zero(t, r.ContentLength)
		writeJSON(t, w, http.StatusOK, versionFixture(ImageVersionStatePublished))
	})
	server := httptest.NewServer(mux)
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

	version, err := catalog.CreateDraftVersion(ctx, image.Name, CreateDraftVersionRequest{Version: "v1.0.0"})
	require.NoError(t, err)
	assert.Equal(t, ImageVersionStateDraft, version.State)

	artifact, err := catalog.AddArtifact(ctx, image.Name, version.Version, AddArtifactRequest{
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               ArtifactFormatQCOW2,
		PrimaryBlobDigest:    testDigest,
		PrimaryBlobSizeBytes: 12,
		PrimaryMediaType:     "application/x-qcow2",
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

	manifest, err := catalog.GetVersionManifest(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assert.Equal(t, ImageVersionStateDraft, manifest.Version.State)
	require.Len(t, manifest.Artifacts, 1)

	published, err := catalog.PublishVersion(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assert.Equal(t, ImageVersionStatePublished, published.State)
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
		Format:               ArtifactFormatQCOW2,
		PrimaryBlobDigest:    testDigest,
		PrimaryBlobSizeBytes: 12,
		PrimaryMediaType:     "application/x-qcow2",
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
