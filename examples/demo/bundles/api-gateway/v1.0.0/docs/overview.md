# API Gateway v1.0.0

Central API gateway handling request routing, authentication enforcement, rate limiting, and CORS. Uses a local policy schema (not ref-based) to demonstrate inline policy definition.

## Interfaces
- **HTTP** on port 8080 (public) - REST API

## Dependencies
- **auth-service** (required) - Token validation
- **orders-service** (required) - Order management
- **payments-service** (required) - Payment processing
