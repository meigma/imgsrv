package publish

import (
	"context"
	"errors"
)

// ServiceConfig configures the publish workflow service.
type ServiceConfig struct {
	// Store persists durable publish workflow state.
	Store Store
}

// Service coordinates client-facing publish workflow operations.
type Service struct {
	store Store
}

// NewService constructs a publish workflow service.
func NewService(config ServiceConfig) *Service {
	return &Service{store: config.Store}
}

// PublishVersion freezes a draft version and queues durable publish steps.
func (service *Service) PublishVersion(ctx context.Context, params EnqueueVersionParams) (Job, error) {
	store, err := service.dependencies()
	if err != nil {
		return Job{}, err
	}

	return store.EnqueueVersion(ctx, params)
}

// GetPublishJob returns a publish job with its durable steps.
func (service *Service) GetPublishJob(ctx context.Context, params GetJobParams) (Job, error) {
	store, err := service.dependencies()
	if err != nil {
		return Job{}, err
	}

	return store.GetJob(ctx, params)
}

func (service *Service) dependencies() (Store, error) {
	if service == nil || service.store == nil {
		return nil, errors.New("publish service is not configured")
	}

	return service.store, nil
}
