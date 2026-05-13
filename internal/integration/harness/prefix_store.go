//go:build integration

package harness

import (
	"context"
	"strings"

	"github.com/meigma/imgsrv/internal/objectstore"
)

type prefixedObjectStore struct {
	inner  objectstore.Store
	prefix string
}

func newPrefixedObjectStore(inner objectstore.Store, prefix string) objectstore.Store {
	return prefixedObjectStore{
		inner:  inner,
		prefix: strings.Trim(prefix, "/") + "/",
	}
}

func (store prefixedObjectStore) CreateMultipartUpload(
	ctx context.Context,
	params objectstore.CreateMultipartUploadParams,
) (objectstore.MultipartUpload, error) {
	if err := params.Validate(); err != nil {
		return objectstore.MultipartUpload{}, err
	}

	params.Key = store.prefixKey(params.Key)
	upload, err := store.inner.CreateMultipartUpload(ctx, params)
	upload.Key = store.stripKey(upload.Key)

	return upload, err
}

func (store prefixedObjectStore) PutPart(
	ctx context.Context,
	params objectstore.PutPartParams,
) (objectstore.Part, error) {
	if err := params.Validate(); err != nil {
		return objectstore.Part{}, err
	}

	params.Key = store.prefixKey(params.Key)

	return store.inner.PutPart(ctx, params)
}

func (store prefixedObjectStore) CompleteMultipartUpload(
	ctx context.Context,
	params objectstore.CompleteMultipartUploadParams,
) (objectstore.ObjectInfo, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}

	params.Key = store.prefixKey(params.Key)
	info, err := store.inner.CompleteMultipartUpload(ctx, params)

	return store.stripObjectInfo(info), err
}

func (store prefixedObjectStore) AbortMultipartUpload(
	ctx context.Context,
	params objectstore.AbortMultipartUploadParams,
) error {
	if err := params.Validate(); err != nil {
		return err
	}

	params.Key = store.prefixKey(params.Key)

	return store.inner.AbortMultipartUpload(ctx, params)
}

func (store prefixedObjectStore) OpenObject(
	ctx context.Context,
	params objectstore.OpenObjectParams,
) (objectstore.ObjectReader, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectReader{}, err
	}

	params.Key = store.prefixKey(params.Key)
	reader, err := store.inner.OpenObject(ctx, params)
	reader.Info = store.stripObjectInfo(reader.Info)

	return reader, err
}

func (store prefixedObjectStore) StatObject(
	ctx context.Context,
	params objectstore.StatObjectParams,
) (objectstore.ObjectInfo, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}

	params.Key = store.prefixKey(params.Key)
	info, err := store.inner.StatObject(ctx, params)

	return store.stripObjectInfo(info), err
}

func (store prefixedObjectStore) CopyObject(
	ctx context.Context,
	params objectstore.CopyObjectParams,
) (objectstore.ObjectInfo, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}

	params.SourceKey = store.prefixKey(params.SourceKey)
	params.DestinationKey = store.prefixKey(params.DestinationKey)
	info, err := store.inner.CopyObject(ctx, params)

	return store.stripObjectInfo(info), err
}

func (store prefixedObjectStore) DeleteObject(
	ctx context.Context,
	params objectstore.DeleteObjectParams,
) error {
	if err := params.Validate(); err != nil {
		return err
	}

	params.Key = store.prefixKey(params.Key)

	return store.inner.DeleteObject(ctx, params)
}

func (store prefixedObjectStore) prefixKey(key string) string {
	return store.prefix + key
}

func (store prefixedObjectStore) stripKey(key string) string {
	return strings.TrimPrefix(key, store.prefix)
}

func (store prefixedObjectStore) stripObjectInfo(info objectstore.ObjectInfo) objectstore.ObjectInfo {
	info.Key = store.stripKey(info.Key)

	return info
}
