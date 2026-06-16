# Auth Service

gRPC-based authentication and authorization service. Manages JWT token lifecycle, session storage in Redis, and user credential validation.

## Interfaces
- **gRPC** on port 50051 (internal)

## Dependencies
- **redis** - Session and token storage

## State
Hybrid - stateless request handling with semi-persistent session data in Redis.
