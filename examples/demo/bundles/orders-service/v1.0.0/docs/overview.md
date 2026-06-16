# Orders Service v1.0.0

Order management service handling the full order lifecycle: creation, fulfillment, and cancellation.

## Interfaces
- **HTTP** on port 8082 (internal) - REST API for order CRUD
- **Events** (internal) - Publishes order lifecycle events

## Dependencies
- **postgresql** (required) - Order persistence
- **payments-service** (required) - Payment processing
- **notification-worker** (optional) - Order notification emails
