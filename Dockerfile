# Build stage
FROM golang:1.25-alpine3.21 AS build

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build binary with version info
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /pacto ./cmd/pacto

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 65532 -h /home/pacto pacto

COPY --from=build /pacto /usr/local/bin/pacto

# Writable cache directory for OCI bundles
RUN mkdir -p /home/pacto/.cache/pacto/oci && chown -R pacto:pacto /home/pacto/.cache

USER pacto
WORKDIR /home/pacto

# Dashboard defaults
ENV PACTO_NO_UPDATE_CHECK=1
EXPOSE 3000

ENTRYPOINT ["pacto"]
CMD ["dashboard", "--port", "3000"]
