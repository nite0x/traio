# syntax=docker/dockerfile:1.7

ARG NODE_VERSION=22
ARG GO_VERSION=1.26

FROM node:${NODE_VERSION}-bookworm-slim AS web-build
WORKDIR /src

# "frontend" is the traio-desktop repository supplied as a BuildKit named
# context: --build-context frontend=../traio-desktop
COPY --from=frontend package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm_config_fetch_retries=5 \
    npm_config_fetch_retry_mintimeout=20000 \
    npm_config_fetch_retry_maxtimeout=120000 \
    npm ci --no-audit --no-fund

COPY --from=frontend index.html tsconfig.json tsconfig.node.json vite.config.ts ./
COPY --from=frontend public ./public
COPY --from=frontend src ./src

# An empty API base makes browser requests same-origin. Explicitly disable the
# desktop repository's demo fallback so the packaged UI uses the real API.
RUN VITE_API_BASE= VITE_DEMO_MODE=false npm run build:web

FROM golang:${GO_VERSION}-bookworm AS server-build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/traio-server ./cmd/server

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install --no-install-recommends -y \
        bash \
        ca-certificates \
        curl \
        default-jre-headless \
        lsof \
        procps \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 traio \
    && useradd --uid 10001 --gid 10001 --home-dir /var/lib/traio \
        --no-create-home --shell /usr/sbin/nologin traio \
    && install -d -o traio -g traio -m 0700 /var/lib/traio \
    && install -d -o root -g root -m 0755 /opt/traio/web

COPY --from=server-build /out/traio-server /usr/local/bin/traio-server
COPY --from=web-build /src/dist/ /opt/traio/web/

ENV GIN_MODE=release \
    TRAIO_DEPLOYMENT_MODE=server \
    TRAIO_RUNTIME_DIR=/var/lib/traio \
    TRAIO_LISTEN_ADDR=0.0.0.0:8080 \
    TRAIO_WEB_DIR=/opt/traio/web \
    TRAIO_IBKR_GATEWAY_LIFECYCLE=managed

USER 10001:10001
WORKDIR /var/lib/traio
VOLUME ["/var/lib/traio"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl --fail --silent --show-error http://127.0.0.1:8080/health >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/traio-server"]
