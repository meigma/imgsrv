// Package catalog defines the image catalog domain boundary.
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Error identifies a category of catalog failure.
type Error string

// Error returns the error kind text.
func (kind Error) Error() string {
	return string(kind)
}

const (
	// ErrConflict means the requested catalog identity already exists.
	ErrConflict Error = "catalog conflict"

	// ErrFailedPrecondition means the operation violates catalog state.
	ErrFailedPrecondition Error = "catalog failed precondition"

	// ErrInvalid means the request contains invalid input.
	ErrInvalid Error = "catalog invalid input"

	// ErrNotFound means the requested catalog resource does not exist.
	ErrNotFound Error = "catalog not found"
)

// Store persists image catalog operations.
type Store interface {
	// CreateImage creates an operator-defined image namespace.
	CreateImage(context.Context, CreateImageParams) (Image, error)

	// CreateDraftVersion creates a mutable draft version for an image.
	CreateDraftVersion(context.Context, CreateDraftVersionParams) (Version, error)

	// AddArtifact adds or replaces a primary artifact on a draft version.
	AddArtifact(context.Context, AddArtifactParams) (Artifact, error)

	// AddAttachment adds or replaces a secondary attachment on a draft version.
	AddAttachment(context.Context, AddAttachmentParams) (Attachment, error)

	// PublishVersion marks a draft version immutable and publishable.
	PublishVersion(context.Context, PublishVersionParams) (Version, error)

	// PutAlias creates or moves an alias to a published version.
	PutAlias(context.Context, PutAliasParams) (Alias, error)

	// GetAlias returns an alias by image and alias name.
	GetAlias(context.Context, GetAliasParams) (Alias, error)

	// ResolveManifest resolves a published image manifest by version or alias.
	ResolveManifest(context.Context, ResolveManifestParams) (Manifest, error)
}

// Digest identifies a verified content blob.
type Digest string

// ParseDigest validates raw as a sha256 digest.
func ParseDigest(raw string) (Digest, error) {
	if !matches(`^sha256:[0-9a-f]{64}$`, raw) {
		return "", fmt.Errorf("%w: digest must match sha256:<64 lowercase hex chars>", ErrInvalid)
	}

	return Digest(raw), nil
}

// String returns the digest string.
func (d Digest) String() string {
	return string(d)
}

// VersionState is the lifecycle state for an image version.
type VersionState string

const (
	// VersionStateDraft means a version manifest can still be edited.
	VersionStateDraft VersionState = "draft"

	// VersionStatePublished means a version manifest is immutable.
	VersionStatePublished VersionState = "published"
)

// ArtifactFormat is a supported primary artifact format.
type ArtifactFormat string

const (
	// ArtifactFormatRaw is a raw disk image artifact.
	ArtifactFormatRaw ArtifactFormat = "raw"

	// ArtifactFormatQCOW2 is a qcow2 disk image artifact.
	ArtifactFormatQCOW2 ArtifactFormat = "qcow2"
)

// Image is an operator-defined image namespace.
type Image struct {
	// ID is the stable database identity for the image.
	ID uuid.UUID

	// Name is the operator-defined unique image name.
	Name string

	// DisplayName is an optional human-friendly image label.
	DisplayName *string

	// Description is optional operator-facing image description text.
	Description *string

	// CreatedAt is when the image was created.
	CreatedAt time.Time

	// UpdatedAt is when mutable image metadata last changed.
	UpdatedAt time.Time
}

// Version is a draft or published version of an image.
type Version struct {
	// ID is the stable database identity for the version.
	ID uuid.UUID

	// ImageID identifies the parent image.
	ImageID uuid.UUID

	// Version is the operator-defined version string.
	Version string

	// State is the draft or published lifecycle state.
	State VersionState

	// PublishedAt is set when the version becomes immutable.
	PublishedAt *time.Time

	// CreatedAt is when the version was created.
	CreatedAt time.Time

	// UpdatedAt is when mutable version metadata last changed.
	UpdatedAt time.Time
}

// Artifact describes a primary release artifact on a version.
type Artifact struct {
	// ID is the stable database identity for the artifact.
	ID uuid.UUID

	// VersionID identifies the parent image version.
	VersionID uuid.UUID

	// OperatingSystem is the artifact operating-system token.
	OperatingSystem string

	// Architecture is the artifact architecture token.
	Architecture string

	// Format is the primary artifact format.
	Format ArtifactFormat

	// PrimaryBlobDigest is the digest of the primary CAS blob.
	PrimaryBlobDigest Digest

	// PrimaryBlobSizeBytes is the expected size of the primary CAS blob.
	PrimaryBlobSizeBytes int64

	// PrimaryMediaType is the media type for the primary blob in this manifest.
	PrimaryMediaType string

	// CreatedAt is when the artifact declaration was created.
	CreatedAt time.Time

	// UpdatedAt is when the artifact declaration last changed.
	UpdatedAt time.Time
}

// Attachment describes a blob attached to a release artifact.
type Attachment struct {
	// ID is the stable database identity for the attachment.
	ID uuid.UUID

	// ArtifactID identifies the parent artifact.
	ArtifactID uuid.UUID

	// Name is the attachment role or name.
	Name string

	// MediaType is the media type for the attachment blob.
	MediaType string

	// BlobDigest is the digest of the attachment CAS blob.
	BlobDigest Digest

	// BlobSizeBytes is the expected size of the attachment CAS blob.
	BlobSizeBytes int64

	// CreatedAt is when the attachment declaration was created.
	CreatedAt time.Time

	// UpdatedAt is when the attachment declaration last changed.
	UpdatedAt time.Time
}

// Alias is a mutable pointer to a published version.
type Alias struct {
	// ID is the stable database identity for the alias.
	ID uuid.UUID

	// ImageID identifies the image that owns the alias.
	ImageID uuid.UUID

	// Alias is the mutable pointer name.
	Alias string

	// VersionID identifies the published target version.
	VersionID uuid.UUID

	// CreatedAt is when the alias was first created.
	CreatedAt time.Time

	// UpdatedAt is when the alias target last changed.
	UpdatedAt time.Time
}

// Manifest is the published catalog view for a version.
type Manifest struct {
	// Image is the image namespace for the manifest.
	Image Image

	// Version is the published version represented by the manifest.
	Version Version

	// Artifacts are the primary artifacts in stable catalog order.
	Artifacts []ManifestArtifact
}

// ManifestArtifact groups a primary artifact with its attachments.
type ManifestArtifact struct {
	// Artifact is the primary release artifact.
	Artifact Artifact

	// Attachments are artifact attachments in stable catalog order.
	Attachments []Attachment
}

// CreateImageParams creates an image namespace.
type CreateImageParams struct {
	// Name is the operator-defined unique image name.
	Name string

	// DisplayName is an optional human-friendly image label.
	DisplayName *string

	// Description is optional operator-facing image description text.
	Description *string
}

// CreateDraftVersionParams creates a mutable draft version.
type CreateDraftVersionParams struct {
	// ImageName identifies the parent image by name.
	ImageName string

	// Version is the operator-defined version string.
	Version string
}

// AddArtifactParams adds a primary artifact to a draft version.
type AddArtifactParams struct {
	// ImageName identifies the parent image by name.
	ImageName string

	// Version identifies the draft image version.
	Version string

	// OperatingSystem is the artifact operating-system token.
	OperatingSystem string

	// Architecture is the artifact architecture token.
	Architecture string

	// Format is the primary artifact format.
	Format ArtifactFormat

	// PrimaryBlobDigest is the digest of the primary CAS blob.
	PrimaryBlobDigest Digest

	// PrimaryBlobSizeBytes is the expected size of the primary CAS blob.
	PrimaryBlobSizeBytes int64

	// PrimaryMediaType is the media type for the primary blob in this manifest.
	PrimaryMediaType string
}

// AddAttachmentParams adds an attachment blob to a draft artifact.
type AddAttachmentParams struct {
	// ArtifactID identifies the parent artifact.
	ArtifactID uuid.UUID

	// Name is the attachment role or name.
	Name string

	// MediaType is the media type for the attachment blob.
	MediaType string

	// BlobDigest is the digest of the attachment CAS blob.
	BlobDigest Digest

	// BlobSizeBytes is the expected size of the attachment CAS blob.
	BlobSizeBytes int64
}

// PublishVersionParams publishes a draft version.
type PublishVersionParams struct {
	// ImageName identifies the parent image by name.
	ImageName string

	// Version identifies the draft image version.
	Version string
}

// PutAliasParams creates or moves an image alias.
type PutAliasParams struct {
	// ImageName identifies the image that owns the alias.
	ImageName string

	// Alias is the mutable pointer name.
	Alias string

	// Version identifies the published target version.
	Version string
}

// GetAliasParams looks up an image alias.
type GetAliasParams struct {
	// ImageName identifies the image that owns the alias.
	ImageName string

	// Alias is the mutable pointer name.
	Alias string
}

// ResolveManifestParams resolves a published manifest by exact version.
type ResolveManifestParams struct {
	// ImageName identifies the parent image by name.
	ImageName string

	// Version identifies the published image version.
	Version string
}

// ValidateImageName validates an image name.
func ValidateImageName(name string) error {
	pattern := `^[a-z0-9][a-z0-9._-]{0,127}$`
	if !matches(pattern, name) {
		return fmt.Errorf("%w: image name must match %s", ErrInvalid, pattern)
	}

	return nil
}

// ValidateVersion validates an image version.
func ValidateVersion(version string) error {
	pattern := `^[A-Za-z0-9][A-Za-z0-9._+:-]{0,127}$`
	if !matches(pattern, version) {
		return fmt.Errorf("%w: version must match %s", ErrInvalid, pattern)
	}

	return nil
}

// ValidateAlias validates an alias.
func ValidateAlias(alias string) error {
	pattern := `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`
	if !matches(pattern, alias) {
		return fmt.Errorf("%w: alias must match %s", ErrInvalid, pattern)
	}

	return nil
}

// ValidateArtifactFormat validates an artifact format.
func ValidateArtifactFormat(format ArtifactFormat) error {
	switch format {
	case ArtifactFormatRaw, ArtifactFormatQCOW2:
		return nil
	default:
		return fmt.Errorf("%w: unsupported artifact format %q", ErrInvalid, format)
	}
}

// ValidateToken validates an operating-system, architecture, or attachment token.
func ValidateToken(field string, value string) error {
	pattern := `^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`
	if !matches(pattern, value) {
		return fmt.Errorf("%w: %s must match %s", ErrInvalid, field, pattern)
	}

	return nil
}

// matches reports whether value matches pattern under [regexp.MatchString],
// treating any compilation error as a non-match.
func matches(pattern string, value string) bool {
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}

// ValidateNonNegativeSize validates that size is not negative.
func ValidateNonNegativeSize(field string, size int64) error {
	if size < 0 {
		return fmt.Errorf("%w: %s must be non-negative", ErrInvalid, field)
	}

	return nil
}

// ValidateRequiredText validates non-empty text after trimming ASCII whitespace.
func ValidateRequiredText(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalid, field)
	}

	return nil
}
