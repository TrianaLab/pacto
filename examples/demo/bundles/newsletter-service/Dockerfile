# syntax=docker/dockerfile:1
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /newsletter-service ./cmd/newsletter-service

FROM gcr.io/distroless/static
COPY --from=build /newsletter-service /newsletter-service
LABEL org.opencontainers.image.title=newsletter-service
USER nonroot
HEALTHCHECK CMD ["/newsletter-service", "healthcheck"]
EXPOSE 8080
ENTRYPOINT ["/newsletter-service"]
