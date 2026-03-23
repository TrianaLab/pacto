# syntax=docker/dockerfile:1

# ── Build stage ──────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /pacto ./cmd/pacto

# ── Runtime stage ────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /pacto /usr/local/bin/pacto

# Dashboard configuration — override via env vars or Helm values.
# All flags are bound to viper: PACTO_DASHBOARD_PORT, PACTO_DASHBOARD_NAMESPACE,
# PACTO_DASHBOARD_DIAGNOSTICS, PACTO_NO_CACHE, PACTO_VERBOSE.
# OCI credentials: PACTO_REGISTRY_USERNAME, PACTO_REGISTRY_PASSWORD, PACTO_REGISTRY_TOKEN.
ENV PACTO_DASHBOARD_PORT=3000
ENV PACTO_NO_UPDATE_CHECK=1

EXPOSE 3000

USER nonroot:nonroot

ENTRYPOINT ["pacto"]
CMD ["dashboard"]
