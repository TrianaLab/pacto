# syntax=docker/dockerfile:1
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /payments ./cmd/payments

FROM gcr.io/distroless/static
COPY --from=build /payments /payments
LABEL org.opencontainers.image.title=payments
LABEL org.opencontainers.image.description="Takes card payments"
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/payments"]
