# Build the manager binary
FROM golang:1.26.5 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
ARG DASHBOARD_IMAGE=

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/

# Build with version metadata injected via ldflags
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w \
      -X main.version=${VERSION} \
      -X main.gitCommit=${GIT_COMMIT} \
      -X main.buildDate=${BUILD_DATE} \
      -X main.dashboardImage=${DASHBOARD_IMAGE}" \
    -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
