# Payments Service — On-Call Runbook

## Overview
The payments-service exposes the Payment Intents API and processes charges via
Stripe with mandatory fraud detection.

## Common incidents
- **Stripe webhook backlog** — check the webhook consumer lag and the
  `WEBHOOK_SECRET` rotation status.
- **Fraud-service unavailable** — payments fail closed; verify fraud-service
  health and Redis connectivity.
- **Database connection saturation** — inspect the PostgreSQL connection pool.

## Escalation
Page `payments-oncall`; escalate to the payments-team DRI for sustained outages.
