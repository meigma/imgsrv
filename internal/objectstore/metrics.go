package objectstore

import (
	"context"
	"errors"
	"time"

	"github.com/meigma/imgsrv/internal/metrics"
)

const (
	objectstoreOperationCreateMultipartUpload   = "create_multipart_upload"
	objectstoreOperationPutPart                 = "put_part"
	objectstoreOperationCompleteMultipartUpload = "complete_multipart_upload"
	objectstoreOperationAbortMultipartUpload    = "abort_multipart_upload"
	objectstoreOperationOpenObject              = "open_object"
	objectstoreOperationStatObject              = "stat_object"
	objectstoreOperationCopyObject              = "copy_object"
	objectstoreOperationDeleteObject            = "delete_object"

	objectstoreDirectionRead  = "read"
	objectstoreDirectionWrite = "write"
)

// InstrumentStore wraps store with object-store operation metrics when recorder is enabled.
func InstrumentStore(store Store, recorder *metrics.Recorder) Store {
	if store == nil || !recorder.Enabled() {
		return store
	}

	return meteredStore{
		next:     store,
		recorder: recorder,
	}
}

type meteredStore struct {
	next     Store
	recorder *metrics.Recorder
}

func (store meteredStore) CreateMultipartUpload(
	ctx context.Context,
	params CreateMultipartUploadParams,
) (MultipartUpload, error) {
	start := time.Now()
	upload, err := store.next.CreateMultipartUpload(ctx, params)
	store.record(ctx, objectstoreOperationCreateMultipartUpload, start, err)

	return upload, err
}

func (store meteredStore) PutPart(ctx context.Context, params PutPartParams) (Part, error) {
	start := time.Now()
	part, err := store.next.PutPart(ctx, params)
	store.record(ctx, objectstoreOperationPutPart, start, err)
	if err == nil {
		store.recorder.RecordObjectstoreBytes(ctx, objectstoreOperationPutPart, objectstoreDirectionWrite, part.SizeBytes)
	}

	return part, err
}

func (store meteredStore) CompleteMultipartUpload(
	ctx context.Context,
	params CompleteMultipartUploadParams,
) (ObjectInfo, error) {
	start := time.Now()
	info, err := store.next.CompleteMultipartUpload(ctx, params)
	store.record(ctx, objectstoreOperationCompleteMultipartUpload, start, err)

	return info, err
}

func (store meteredStore) AbortMultipartUpload(ctx context.Context, params AbortMultipartUploadParams) error {
	start := time.Now()
	err := store.next.AbortMultipartUpload(ctx, params)
	store.record(ctx, objectstoreOperationAbortMultipartUpload, start, err)

	return err
}

func (store meteredStore) OpenObject(ctx context.Context, params OpenObjectParams) (ObjectReader, error) {
	start := time.Now()
	reader, err := store.next.OpenObject(ctx, params)
	store.record(ctx, objectstoreOperationOpenObject, start, err)
	if err == nil {
		store.recorder.RecordObjectstoreBytes(
			ctx,
			objectstoreOperationOpenObject,
			objectstoreDirectionRead,
			openedObjectBytes(params, reader.Info),
		)
	}

	return reader, err
}

func (store meteredStore) StatObject(ctx context.Context, params StatObjectParams) (ObjectInfo, error) {
	start := time.Now()
	info, err := store.next.StatObject(ctx, params)
	store.record(ctx, objectstoreOperationStatObject, start, err)

	return info, err
}

func (store meteredStore) CopyObject(ctx context.Context, params CopyObjectParams) (ObjectInfo, error) {
	start := time.Now()
	info, err := store.next.CopyObject(ctx, params)
	store.record(ctx, objectstoreOperationCopyObject, start, err)
	if err == nil {
		store.recorder.RecordObjectstoreBytes(
			ctx,
			objectstoreOperationCopyObject,
			objectstoreDirectionRead,
			info.SizeBytes,
		)
		store.recorder.RecordObjectstoreBytes(
			ctx,
			objectstoreOperationCopyObject,
			objectstoreDirectionWrite,
			info.SizeBytes,
		)
	}

	return info, err
}

func (store meteredStore) DeleteObject(ctx context.Context, params DeleteObjectParams) error {
	start := time.Now()
	err := store.next.DeleteObject(ctx, params)
	store.record(ctx, objectstoreOperationDeleteObject, start, err)

	return err
}

func (store meteredStore) record(ctx context.Context, operation string, start time.Time, err error) {
	store.recorder.RecordObjectstoreOperation(ctx, operation, objectstoreOutcome(err), time.Since(start))
}

func objectstoreOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	default:
		return "error"
	}
}

func openedObjectBytes(params OpenObjectParams, info ObjectInfo) int64 {
	if info.SizeBytes <= 0 {
		return 0
	}
	if params.Range == nil {
		return info.SizeBytes
	}
	if params.Range.OpenEnded {
		if params.Range.Start >= info.SizeBytes {
			return 0
		}
		return info.SizeBytes - params.Range.Start
	}

	return min(params.Range.End-params.Range.Start+1, info.SizeBytes)
}
