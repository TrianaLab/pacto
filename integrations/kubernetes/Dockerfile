# syntax=docker/dockerfile:1
# Monorepo-aware build: context is the repository ROOT so the operator module
# resolves the engine (github.com/trianalab/pacto/v3) through the workspace
# (go.work) instead of a published tag — no replace, no unreleased-tag dependency.
# Pin the builder to the BUILD platform so `go build` runs NATIVELY and
# cross-compiles to $TARGETARCH (CGO_ENABLED=0 below) instead of running under QEMU
# emulation for the non-native arch — the arm64 build was the multi-arch release
# pole. The distroless/static runtime stage has no RUN, so nothing executes under the
# target platform. Matches the root Dockerfile's cross-compile pattern.
FROM --platform=$BUILDPLATFORM golang:1.26.6 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
ARG DASHBOARD_IMAGE=
# RELEASE_BUILD=1 (set by the release publisher) builds the operator module
# STANDALONE with GOWORK=off, so it resolves the engine core from the PUBLISHED
# version pinned in integrations/kubernetes/go.mod — the same dependency graph an
# external consumer of the module gets — never the local workspace tree. Dev, kind
# and the staging dry-run leave it unset and build through go.work (the local core,
# which for those flows is the same commit being released).
ARG RELEASE_BUILD=

WORKDIR /workspace
# Workspace + both module manifests first (cache-friendly).
COPY go.work ./
COPY go.mod go.sum ./
COPY integrations/kubernetes/go.mod integrations/kubernetes/go.sum integrations/kubernetes/
COPY pkg/ pkg/
COPY internal/ internal/
COPY integrations/kubernetes/ integrations/kubernetes/
RUN set -eu; \
    LDFLAGS="-s -w -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT} -X main.buildDate=${BUILD_DATE} -X main.dashboardImage=${DASHBOARD_IMAGE}"; \
    if [ -n "${RELEASE_BUILD}" ]; then \
      echo "release build: GOWORK=off against the published pinned core"; \
      cd integrations/kubernetes; \
      GOWORK=off GOFLAGS=-mod=mod GOPROXY=direct GOPRIVATE='github.com/trianalab/*' GONOSUMDB='github.com/trianalab/*' go mod download; \
      CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} GOWORK=off GOFLAGS=-mod=mod GOPROXY=direct GOPRIVATE='github.com/trianalab/*' GONOSUMDB='github.com/trianalab/*' \
        go build -ldflags "${LDFLAGS}" -o /workspace/manager ./cmd; \
    else \
      echo "workspace build: go.work resolves the local core"; \
      go work sync && go mod download all; \
      CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
        go build -ldflags "${LDFLAGS}" -o /workspace/manager ./integrations/kubernetes/cmd; \
    fi

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
