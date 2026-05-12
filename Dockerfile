# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.2

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    targetos="${TARGETOS:-linux}" && \
    targetarch="${TARGETARCH:-$(go env GOARCH)}" && \
    CGO_ENABLED=0 GOOS="${targetos}" GOARCH="${targetarch}" \
    go build \
      -trimpath \
      -buildvcs=false \
      -tags=netgo,osusergo \
      -ldflags="-s -w" \
      -o /out/imgsrv \
      ./cmd/imgsrv

FROM scratch

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown

LABEL org.opencontainers.image.title="imgsrv" \
      org.opencontainers.image.description="Image artifact service" \
      org.opencontainers.image.source="https://github.com/meigma/imgsrv" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="Apache-2.0 OR MIT"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/imgsrv /imgsrv

ENV IMGSRV_LISTEN=:8080 \
    IMGSRV_LOG_FORMAT=json \
    IMGSRV_METRICS_LISTEN=:9464

EXPOSE 8080 9464

USER 65532:65532
STOPSIGNAL SIGTERM

ENTRYPOINT ["/imgsrv"]
