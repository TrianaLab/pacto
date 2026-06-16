# Payments Service v1.1.0

Adds Stripe webhook handling and payment failure tracking.

## Changes from v1.0.0
- Added POST /webhooks/stripe endpoint
- Added payment.failed event
- Added WEBHOOK_SECRET config (optional)
