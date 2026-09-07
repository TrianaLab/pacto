# Payments Service v2.0.0 (BREAKING)

Major version upgrade replacing the charges API with Payment Intents. Fraud detection is now mandatory.

## BREAKING CHANGES from v1.x
- **Removed** `/charges` endpoint -> replaced by `/payment-intents`
- **Renamed** `STRIPE_API_KEY` -> `STRIPE_SECRET_KEY`
- **Required** `WEBHOOK_SECRET` (was optional)
- **Required** `fraud-service` dependency (was optional)
- **Removed** `ENABLE_REFUNDS` config (refunds always enabled)
- **Changed** upgrade strategy from rolling to blue-green
- **Changed** events from payment.completed/failed to payment.intent.created/succeeded/failed

## New Features
- Payment Intents with confirm/cancel lifecycle
- Cursor-based pagination for listing intents
- Mandatory fraud scoring on all payment creation
- Risk signals array in responses
