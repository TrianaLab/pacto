# syntax=docker/dockerfile:1
# Monorepo-aware build: context is the repository ROOT so the operator module
# resolves the engine (github.com/trianalab/pacto/v3) through the workspace
# (go.work) instead of a published tag — no replace, no unreleased-tag dependency.
FROM golang:1.26.5 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
ARG DASHBOARD_IMAGE=

WORKDIR /workspace
# Workspace + both module manifests first (cache-friendly).
COPY go.work ./
COPY go.mod go.sum ./
COPY integrations/kubernetes/go.mod integrations/kubernetes/go.sum integrations/kubernetes/
RUN go work sync && go mod download all
# Source (only the engine core packages the operator needs + the integration).
COPY pkg/ pkg/
COPY internal/ internal/
COPY integrations/kubernetes/ integrations/kubernetes/
# Build the operator via the workspace; go.work resolves pacto/v2 locally.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w \
      -X main.version=${VERSION} \
      -X main.gitCommit=${GIT_COMMIT} \
      -X main.buildDate=${BUILD_DATE} \
      -X main.dashboardImage=${DASHBOARD_IMAGE}" \
    -o manager ./integrations/kubernetes/cmd

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
