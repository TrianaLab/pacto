# Build stage — uses Go's native cross-compilation (no QEMU needed)
FROM --platform=$BUILDPLATFORM golang:1.25.7-alpine3.22 AS build

ARG TARGETARCH

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build binary with version info
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
COPY . .
RUN CGO_ENABLED=0 GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /pacto ./cmd/pacto

# Runtime stage
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 65532 -h /home/pacto pacto

COPY --from=build /pacto /usr/local/bin/pacto

# Writable cache directory for OCI bundles
RUN mkdir -p /home/pacto/.cache/pacto/oci && chown -R pacto:pacto /home/pacto/.cache

USER pacto
WORKDIR /home/pacto

# Dashboard defaults
ENV PACTO_NO_UPDATE_CHECK=1
ENV PACTO_DASHBOARD_HOST=0.0.0.0
EXPOSE 3000

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:3000/health || exit 1

ENTRYPOINT ["pacto"]
CMD ["dashboard"]
