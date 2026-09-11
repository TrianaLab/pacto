# Build stage — uses Go's native cross-compilation (no QEMU needed)
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine3.23 AS build

ARG TARGETARCH

WORKDIR /src

# Cache dependencies. `go mod download` is one network round trip per module
# against proxy.golang.org and retries nothing itself, so a single dropped
# connection fails the whole image build — that is what took ci-e2e-compose down
# on 2026-09-11. release/scripts/retry.sh cannot be used here: it is not in this
# stage's copied context. tests/release/workflow_tooling_test.go holds the inline
# loop to the same contract.
COPY go.mod go.sum ./
RUN set -eu; \
    for attempt in 1 2 3 4 5; do \
      if go mod download; then break; fi; \
      if [ "$attempt" = 5 ]; then echo "go mod download failed after 5 attempts" >&2; exit 1; fi; \
      echo "go mod download attempt $attempt failed; retrying in $((attempt * 5))s" >&2; \
      sleep $((attempt * 5)); \
    done

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

# `apk add` never touches packages the base image already ships, so an openssl
# CVE fixed in the branch repo (libssl3/libcrypto3 3.5.7-r0 -> 3.5.8-r0) stays in
# the image until the alpine tag is respun. Upgrade first so the Trivy gate sees
# the patched branch, not the frozen snapshot.
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 65532 -h /home/pacto pacto

COPY --from=build /pacto /usr/local/bin/pacto

# Writable cache directory for OCI bundles
RUN mkdir -p /home/pacto/.cache/pacto/oci && chown -R pacto:pacto /home/pacto/.cache

USER pacto
WORKDIR /home/pacto

# Explicit, so the OCI cache resolves to the mounted writable directory even when
# a pod securityContext sets runAsUser and the runtime never consults /etc/passwd
# for HOME. Without it the cache is silently disabled on a read-only root.
ENV HOME=/home/pacto

# Dashboard defaults
ENV PACTO_NO_UPDATE_CHECK=1
ENV PACTO_DASHBOARD_HOST=0.0.0.0
EXPOSE 3000

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:3000/health || exit 1

ENTRYPOINT ["pacto"]
CMD ["dashboard"]
