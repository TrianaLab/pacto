# syntax=docker/dockerfile:1
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN go build -o /app ./cmd/app

FROM gcr.io/distroless/static
LABEL org.opencontainers.image.title=orders \
      org.opencontainers.image.description="Takes and tracks orders"
COPY --from=build /app /app
HEALTHCHECK --interval=30s CMD curl -f http://localhost:8080/healthz?token=tok3n || exit 1
EXPOSE 8080 9090/udp
ENTRYPOINT ["/app"]
