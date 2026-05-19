# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.3-bookworm@sha256:252599aeb51ad60b83e4d8821802068127c528c707cb7dd7afd93be057c6011c AS deps
WORKDIR /src

ENV CGO_ENABLED=0

COPY .go-version go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    expected="$(cat .go-version)" && \
    actual="$(go env GOVERSION)" && \
    actual="${actual#go}" && \
    if [ "${expected}" != "${actual}" ]; then \
      echo "Go builder version ${actual} does not match .go-version ${expected}" >&2; \
      exit 1; \
    fi && \
    go mod download

FROM deps AS source
COPY . .

FROM source AS test
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test -mod=readonly ./...

FROM source AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build \
      -mod=readonly \
      -trimpath \
      -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/imgsrv \
      ./cmd/imgsrv

FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639 AS runtime

ARG VERSION=dev
ARG COMMIT=none
ARG SOURCE=https://github.com/meigma/imgsrv

LABEL org.opencontainers.image.title="imgsrv" \
      org.opencontainers.image.description="Image artifact service" \
      org.opencontainers.image.source="${SOURCE}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="Apache-2.0 OR MIT"

ENV IMGSRV_LISTEN=:8080 \
    IMGSRV_LOG_FORMAT=json \
    IMGSRV_METRICS_LISTEN=:9464

USER 65532:65532
COPY --from=build /out/imgsrv /usr/local/bin/imgsrv

EXPOSE 8080 9464
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/imgsrv"]
