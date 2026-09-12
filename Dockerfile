# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27.1
ARG NODE_VERSION=24

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
COPY internal/modules/investment/chart/ /src/internal/modules/investment/chart/
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY scripts/compile.sh ./scripts/compile.sh
COPY --from=web /src/internal/webui/dist/ ./internal/webui/dist/
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH OUTPUT=/out/workbench ./scripts/compile.sh
RUN mkdir -p /runtime/data /runtime/tmp && chmod 0700 /runtime/data && chmod 1777 /runtime/tmp

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /runtime/ /
COPY --from=build /out/workbench /workbench
ENV HOME=/data WORKBENCH_LISTEN=0.0.0.0:8080 WORKBENCH_DATA=/data/data.db WORKBENCH_AUTH=password
USER 65532:65532
WORKDIR /data
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/workbench", "-healthcheck"]
ENTRYPOINT ["/workbench"]
