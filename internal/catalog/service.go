package catalog

import (
	"context"
	"errors"
)

// ServiceConfig configures a catalog service.
type ServiceConfig struct {
	// Store persists catalog state.
	Store Store
}

// Service coordinates image catalog operations.
type Service struct {
	// store persists catalog state.
	store Store
}

// NewService constructs a catalog service from config.
func NewService(config ServiceConfig) *Service {
	return &Service{store: config.Store}
}

// CreateImage creates an operator-defined image namespace.
func (service *Service) CreateImage(ctx context.Context, params CreateImageParams) (Image, error) {
	store, err := service.dependencies()
	if err != nil {
		return Image{}, err
	}

	return store.CreateImage(ctx, params)
}

// ListImages returns image namespaces with published versions in stable order.
func (service *Service) ListImages(ctx context.Context, params ListImagesParams) ([]Image, error) {
	store, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	return store.ListImages(ctx, params)
}

// GetImage returns one image namespace by name when it has a published version.
func (service *Service) GetImage(ctx context.Context, params GetImageParams) (Image, error) {
	store, err := service.dependencies()
	if err != nil {
		return Image{}, err
	}

	return store.GetImage(ctx, params)
}

// CreateDraftVersion creates a mutable draft version for an image.
func (service *Service) CreateDraftVersion(
	ctx context.Context,
	params CreateDraftVersionParams,
) (Version, error) {
	store, err := service.dependencies()
	if err != nil {
		return Version{}, err
	}

	return store.CreateDraftVersion(ctx, params)
}

// ListVersions returns published versions for one image in stable order.
func (service *Service) ListVersions(ctx context.Context, params ListVersionsParams) ([]Version, error) {
	store, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	return store.ListVersions(ctx, params)
}

// ListPublishedArtifacts returns artifacts for an exact published image version.
func (service *Service) ListPublishedArtifacts(
	ctx context.Context,
	params ListPublishedArtifactsParams,
) ([]Artifact, error) {
	store, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	return store.ListPublishedArtifacts(ctx, params)
}

// GetPublishedArtifact returns one artifact for an exact published image version.
func (service *Service) GetPublishedArtifact(
	ctx context.Context,
	params GetPublishedArtifactParams,
) (Artifact, error) {
	store, err := service.dependencies()
	if err != nil {
		return Artifact{}, err
	}

	return store.GetPublishedArtifact(ctx, params)
}

// GetPublishedAttachment returns one attachment for an exact published image version artifact.
func (service *Service) GetPublishedAttachment(
	ctx context.Context,
	params GetPublishedAttachmentParams,
) (Attachment, error) {
	store, err := service.dependencies()
	if err != nil {
		return Attachment{}, err
	}

	return store.GetPublishedAttachment(ctx, params)
}

// AddArtifact adds a primary artifact on a draft version.
func (service *Service) AddArtifact(ctx context.Context, params AddArtifactParams) (Artifact, error) {
	store, err := service.dependencies()
	if err != nil {
		return Artifact{}, err
	}

	return store.AddArtifact(ctx, params)
}

// AddAttachment adds a secondary attachment on a draft version.
func (service *Service) AddAttachment(ctx context.Context, params AddAttachmentParams) (Attachment, error) {
	store, err := service.dependencies()
	if err != nil {
		return Attachment{}, err
	}

	return store.AddAttachment(ctx, params)
}

// DeleteArtifact removes a primary artifact from a draft version.
func (service *Service) DeleteArtifact(ctx context.Context, params DeleteArtifactParams) error {
	store, err := service.dependencies()
	if err != nil {
		return err
	}

	return store.DeleteArtifact(ctx, params)
}

// DeleteAttachment removes an attachment from a draft artifact.
func (service *Service) DeleteAttachment(ctx context.Context, params DeleteAttachmentParams) error {
	store, err := service.dependencies()
	if err != nil {
		return err
	}

	return store.DeleteAttachment(ctx, params)
}

// PutAlias creates or moves an alias to a published version.
func (service *Service) PutAlias(ctx context.Context, params PutAliasParams) (Alias, error) {
	store, err := service.dependencies()
	if err != nil {
		return Alias{}, err
	}

	return store.PutAlias(ctx, params)
}

// ListAliases returns aliases for one image in stable order.
func (service *Service) ListAliases(ctx context.Context, params ListAliasesParams) ([]Alias, error) {
	store, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	return store.ListAliases(ctx, params)
}

// GetAlias returns an alias by image and alias name.
func (service *Service) GetAlias(ctx context.Context, params GetAliasParams) (Alias, error) {
	store, err := service.dependencies()
	if err != nil {
		return Alias{}, err
	}

	return store.GetAlias(ctx, params)
}

// DeleteAlias removes an image alias.
func (service *Service) DeleteAlias(ctx context.Context, params DeleteAliasParams) error {
	store, err := service.dependencies()
	if err != nil {
		return err
	}

	return store.DeleteAlias(ctx, params)
}

// GetVersionManifest resolves an exact draft or published image version manifest.
func (service *Service) GetVersionManifest(
	ctx context.Context,
	params GetVersionManifestParams,
) (Manifest, error) {
	store, err := service.dependencies()
	if err != nil {
		return Manifest{}, err
	}

	return store.GetVersionManifest(ctx, params)
}

// ResolveManifest resolves a published image manifest by exact version or alias.
func (service *Service) ResolveManifest(ctx context.Context, params ResolveManifestParams) (Manifest, error) {
	store, err := service.dependencies()
	if err != nil {
		return Manifest{}, err
	}

	return store.ResolveManifest(ctx, params)
}

// dependencies returns the configured store or an error when the service is not usable.
func (service *Service) dependencies() (Store, error) {
	if service == nil || service.store == nil {
		return nil, errors.New("catalog store is not configured")
	}

	return service.store, nil
}
