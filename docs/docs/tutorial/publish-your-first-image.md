---
title: Publish your first image
description: An end-to-end walkthrough that takes an imgsrv deployment from blank to a published, aliased image version.
sidebar_position: 1
---

# Publish your first image

This tutorial takes you from a fresh `imgsrv` deployment to a published image
version with a `latest` alias pointing at it. The goal is to make the
publishing model click by completing the full flow once. We will skip the
exhaustive option lists and the "why" — for those, see
[Publishing model](../concepts/publishing-model.md).

You will:

1. Start `imgsrv` locally against PostgreSQL and MinIO.
2. Capture the bootstrap token and create a `content-writer` principal.
3. Upload a primary artifact and an attachment into CAS.
4. Create an image, a draft version, and attach the artifact + attachment.
5. Publish the version and wait for the publish job to succeed.
6. Move a `latest` alias to the new version.
7. Download the published artifact back.

The bytes you upload do not need to be a real image file — this tutorial uses
a 10 MiB placeholder. The flow is what we are learning.

## Prerequisites

Docker (or Podman with `docker` alias), `curl`, `jq`, and `openssl`. About 15
minutes.

## 1. Start the dependencies

PostgreSQL:

```bash
docker run -d --name imgsrv-pg \
  -p 5432:5432 \
  -e POSTGRES_USER=imgsrv \
  -e POSTGRES_PASSWORD=imgsrv \
  -e POSTGRES_DB=imgsrv \
  postgres:17
```

MinIO with a single bucket:

```bash
docker run -d --name imgsrv-minio \
  -p 9000:9000 -p 9001:9001 \
  -e MINIO_ROOT_USER=imgsrv \
  -e MINIO_ROOT_PASSWORD=imgsrv-secret \
  minio/minio server /data --console-address ':9001'

# Wait a moment, then create the bucket.
docker run --rm --network=host \
  -e AWS_ACCESS_KEY_ID=imgsrv \
  -e AWS_SECRET_ACCESS_KEY=imgsrv-secret \
  amazon/aws-cli \
  --endpoint-url http://127.0.0.1:9000 \
  s3 mb s3://imgsrv
```

## 2. Start imgsrv

In a new terminal, start `imgsrv` pointing at both:

```bash
docker run --rm --network=host --name imgsrv \
  -e IMGSRV_POSTGRES_URL='postgres://imgsrv:imgsrv@127.0.0.1:5432/imgsrv?sslmode=disable' \
  -e IMGSRV_S3_ENDPOINT='127.0.0.1:9000' \
  -e IMGSRV_S3_BUCKET=imgsrv \
  -e IMGSRV_S3_ACCESS_KEY_ID=imgsrv \
  -e IMGSRV_S3_SECRET_ACCESS_KEY=imgsrv-secret \
  -e IMGSRV_S3_PATH_STYLE=true \
  -e IMGSRV_CAS_PROMOTION_ENABLED=true \
  ghcr.io/meigma/imgsrv:latest
```

Watch the log line for the bootstrap token. It looks like:

```
imgsrv bootstrap token (one-time): imgsrv_at_…
```

Copy it. We will use it as `$BOOTSTRAP_TOKEN` in the rest of the tutorial. In
a third terminal:

```bash
export BOOTSTRAP_TOKEN='imgsrv_at_…'   # paste the token here
export IMGSRV='http://127.0.0.1:8080'

# Sanity check.
curl -sf "$IMGSRV/healthz" -o /dev/null && echo 'imgsrv is up'
```

## 3. Create a content-writer principal

The bootstrap token grants `auth-manager`. Use it to create a service
principal that will publish:

```bash
PRINCIPAL_ID=$(curl -sf -X POST "$IMGSRV/v1/auth/principals" \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"kind": "service", "display_name": "tutorial-publisher"}' \
  | jq -r .id)

ROLE_ID=$(curl -sf "$IMGSRV/v1/auth/roles" \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN" \
  | jq -r '.roles[] | select(.name == "content-writer") | .id')

curl -sf -X PUT \
  "$IMGSRV/v1/auth/principals/$PRINCIPAL_ID/roles/$ROLE_ID" \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN"

TOKEN=$(curl -sf -X POST \
  "$IMGSRV/v1/auth/principals/$PRINCIPAL_ID/api-tokens" \
  -H "Authorization: Bearer $BOOTSTRAP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name": "tutorial", "expires_at": "2027-01-01T00:00:00Z"}' \
  | jq -r .plaintext)

echo "content-writer token: $TOKEN"
```

`$TOKEN` is the bearer credential for the rest of the tutorial.

## 4. Upload the primary artifact

Generate a 10 MiB placeholder file and compute its digest:

```bash
dd if=/dev/urandom of=placeholder.qcow2 bs=1M count=10 status=none
DIGEST=$(openssl dgst -sha256 placeholder.qcow2 | awk '{print $2}')
SIZE=$(wc -c < placeholder.qcow2)

echo "digest: $DIGEST   size: $SIZE"
```

Begin the upload, then send the file as a single part:

```bash
UPLOAD_ID=$(curl -sf -X POST "$IMGSRV/v1/uploads" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"expected_digest": "sha256:'"$DIGEST"'", "expected_size_bytes": '"$SIZE"'}' \
  | jq -r .id)

# Upload part 1 and capture the assigned etag + accepted size.
PART_RESPONSE=$(curl -sf -X PUT \
  "$IMGSRV/v1/uploads/$UPLOAD_ID/parts/1" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary "@placeholder.qcow2")

PART_ETAG=$(echo "$PART_RESPONSE" | jq -r .etag)
PART_SIZE=$(echo "$PART_RESPONSE" | jq -r .size_bytes)

# Complete the upload.
curl -sf -X POST \
  "$IMGSRV/v1/uploads/$UPLOAD_ID/complete" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"parts": [{"number": 1, "etag": "'"$PART_ETAG"'", "size_bytes": '"$PART_SIZE"'}]}'
```

Poll until the upload is `ready`:

```bash
until [ "$(curl -sf "$IMGSRV/v1/uploads/$UPLOAD_ID" -H "Authorization: Bearer $TOKEN" | jq -r .state)" = "ready" ]; do
  sleep 1
done
echo 'primary artifact upload is ready'
```

## 5. Upload the attachment

Repeat for a small placeholder attachment:

```bash
echo 'placeholder attachment' > placeholder.txt
ATT_DIGEST=$(openssl dgst -sha256 placeholder.txt | awk '{print $2}')
ATT_SIZE=$(wc -c < placeholder.txt)

ATT_UPLOAD_ID=$(curl -sf -X POST "$IMGSRV/v1/uploads" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"expected_digest": "sha256:'"$ATT_DIGEST"'", "expected_size_bytes": '"$ATT_SIZE"'}' \
  | jq -r .id)

ATT_PART=$(curl -sf -X PUT \
  "$IMGSRV/v1/uploads/$ATT_UPLOAD_ID/parts/1" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary "@placeholder.txt")

curl -sf -X POST "$IMGSRV/v1/uploads/$ATT_UPLOAD_ID/complete" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"parts": [{"number": 1, "etag": "'"$(echo "$ATT_PART" | jq -r .etag)"'", "size_bytes": '"$(echo "$ATT_PART" | jq -r .size_bytes)"'}]}'

until [ "$(curl -sf "$IMGSRV/v1/uploads/$ATT_UPLOAD_ID" -H "Authorization: Bearer $TOKEN" | jq -r .state)" = "ready" ]; do
  sleep 1
done
echo 'attachment upload is ready'
```

## 6. Create the image and draft version

The image is the namespace; the version is the unit of release meaning.

```bash
curl -sf -X POST "$IMGSRV/v1/images" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name": "tutorial"}'

curl -sf -X POST "$IMGSRV/v1/images/tutorial/versions" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"version": "0.1.0"}'
```

## 7. Attach the artifact and the attachment

Attach the primary artifact by digest:

```bash
ARTIFACT_ID=$(curl -sf -X POST \
  "$IMGSRV/v1/images/tutorial/versions/0.1.0/artifacts" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "operating_system": "linux",
    "architecture": "amd64",
    "format": "qcow2",
    "primary_blob_digest": "sha256:'"$DIGEST"'",
    "primary_blob_size_bytes": '"$SIZE"',
    "primary_media_type": "application/x-qemu-disk"
  }' \
  | jq -r .id)

curl -sf -X POST \
  "$IMGSRV/v1/images/tutorial/versions/0.1.0/artifacts/$ARTIFACT_ID/attachments" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "incus.tar.xz",
    "media_type": "application/x-incus-metadata",
    "blob_digest": "sha256:'"$ATT_DIGEST"'",
    "blob_size_bytes": '"$ATT_SIZE"'
  }'
```

## 8. Publish

Publishing freezes the manifest and enqueues the durable publish job:

```bash
JOB_ID=$(curl -sf -X POST \
  "$IMGSRV/v1/images/tutorial/versions/0.1.0/publish" \
  -H "Authorization: Bearer $TOKEN" \
  | jq -r .job_id)

until STATE=$(curl -sf "$IMGSRV/v1/publish-jobs/$JOB_ID" -H "Authorization: Bearer $TOKEN" | jq -r .state) && [ "$STATE" = "succeeded" -o "$STATE" = "failed" ]; do
  sleep 1
done
echo "publish job: $STATE"
```

You should see `publish job: succeeded`. If you see `failed`, inspect the
response body — the `failed_step` field tells you where the publish stopped.

## 9. Move the latest alias

Aliases are mutable pointers from a name to one published version.

```bash
curl -sf -X PUT \
  "$IMGSRV/v1/images/tutorial/aliases/latest" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"version": "0.1.0"}'
```

## 10. Download it back

Read endpoints are unauthenticated:

```bash
curl -sf "$IMGSRV/v1/images/tutorial/aliases/latest" | jq

curl -fL -o downloaded.qcow2 \
  "$IMGSRV/v1/images/tutorial/versions/0.1.0/artifacts/$ARTIFACT_ID/download"

diff -q placeholder.qcow2 downloaded.qcow2 && echo 'bytes match'
```

Identical bytes, served back through the same service URL the consumer would
have used in production. The publish boundary held: the version is
`published`, the alias points at it, and the artifact is reachable.

## What you have learned

- Uploads populate CAS for a digest, independent of any release.
- Versions start as drafts that can edit their manifest freely.
- Publishing is the immutability boundary — once `succeeded`, the manifest
  cannot change.
- Aliases let consumers track moving names like `latest` without giving up
  the immutability of underlying versions.

The conceptual frame behind this flow is in
[Publishing model](../concepts/publishing-model.md). The prescriptive how-to
that goes deeper (multi-artifact versions, real recovery patterns, OIDC
publishers) is [Publish image versions](../publishers/publish-image-versions.md).

## Clean up

```bash
docker rm -f imgsrv imgsrv-pg imgsrv-minio
rm placeholder.qcow2 placeholder.txt downloaded.qcow2
```
