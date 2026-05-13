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
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/afscp-export-gateway ./cmd/afscp-export-gateway

FROM scratch AS jvs

ARG JVS_VERSION=v0.4.9
ARG JVS_ASSET=jvs-linux-amd64
ARG JVS_SHA256=0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944

ADD --checksum=sha256:0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944 https://github.com/agentsmith-project/jvs/releases/download/v0.4.9/jvs-linux-amd64 /jvs

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown

LABEL org.opencontainers.image.title="AFSCP" \
      org.opencontainers.image.description="Agentsmith filesystem control plane" \
      org.opencontainers.image.source="https://github.com/agentsmith-project/agentsmith-fs-control-plane" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="Apache-2.0"

ENV AFSCP_JVS_BINARY_PATH="/usr/local/bin/jvs" \
    AFSCP_JVS_BINARY_SHA256="0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944"

COPY --from=build /out/afscp-api /usr/local/bin/afscp-api
COPY --from=build /out/afscp-worker /usr/local/bin/afscp-worker
COPY --from=build /out/afscp-export-gateway /usr/local/bin/afscp-export-gateway
COPY --from=jvs --chmod=0755 /jvs /usr/local/bin/jvs

USER nonroot:nonroot
CMD ["/usr/local/bin/afscp-api"]
