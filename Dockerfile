# syntax=docker/dockerfile:1.7

FROM golang:1.22-bookworm AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN export CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" && \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/afscp-api ./cmd/afscp-api && \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/afscp-worker ./cmd/afscp-worker && \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/afscp-export-gateway ./cmd/afscp-export-gateway && \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/afscp-migrate ./cmd/afscp-migrate && \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/afscp-volume-bootstrap ./cmd/afscp-volume-bootstrap

FROM scratch AS jvs

ARG JVS_VERSION=pre-ga-local-afscp-direct-2026-05-16
ARG JVS_ASSET=afscp-jvs-direct-local-linux-amd64
ARG JVS_SHA256=affa86a08dbb2195f594be0be01e9c3f128806f75d04826030afbe4ba283f2e2
ARG JVS_SOURCE_REF=jvs@main:eb026cc48efb57ef64c9f3e482f0011b9232701b
ARG JVS_LOCAL_BINARY=dist/jvs-linux-amd64

# Pre-GA direct AFSCP JVS has no formal release URL yet. Build pipelines must
# place the verified local direct-capable binary in the Docker build context.
COPY --chmod=0755 ${JVS_LOCAL_BINARY} /jvs

# Pinned JVS is dynamically linked and needs the glibc loader in the final image.
FROM gcr.io/distroless/base-debian12:nonroot

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
ARG JVS_SHA256=affa86a08dbb2195f594be0be01e9c3f128806f75d04826030afbe4ba283f2e2
ARG JVS_SOURCE_REF=jvs@main:eb026cc48efb57ef64c9f3e482f0011b9232701b

LABEL org.opencontainers.image.title="AFSCP" \
      org.opencontainers.image.description="Agentsmith filesystem control plane" \
      org.opencontainers.image.source="https://github.com/agentsmith-project/agentsmith-fs-control-plane" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="Apache-2.0"

ENV AFSCP_JVS_BINARY_PATH="/usr/local/bin/jvs" \
    AFSCP_JVS_BINARY_SHA256="${JVS_SHA256}" \
    AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256="${JVS_SHA256}" \
    AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF="${JVS_SOURCE_REF}"

COPY --from=build /out/afscp-api /usr/local/bin/afscp-api
COPY --from=build /out/afscp-worker /usr/local/bin/afscp-worker
COPY --from=build /out/afscp-export-gateway /usr/local/bin/afscp-export-gateway
COPY --from=build /out/afscp-migrate /usr/local/bin/afscp-migrate
COPY --from=build /out/afscp-volume-bootstrap /usr/local/bin/afscp-volume-bootstrap
COPY --from=jvs --chmod=0755 /jvs /usr/local/bin/jvs

USER nonroot:nonroot
CMD ["/usr/local/bin/afscp-api"]
