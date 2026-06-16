# Payments Service v1.0.0

Core payment processing service. Handles charge creation, retrieval, and refunds via Stripe.

## Interfaces
- **HTTP** on port 8083 (internal) - REST API for charges and refunds
- **Events** (internal) - Publishes payment lifecycle events

## Dependencies
- **postgresql** (required) - Payment transaction persistence
- **stripe-api** (required) - External payment processing

## Endpoints
- POST /charges - Create a charge
- GET /charges/{id} - Get charge details
- POST /refunds - Create a refund
