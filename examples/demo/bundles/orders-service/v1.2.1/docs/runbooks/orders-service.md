# Orders Service — On-Call Runbook

## Overview
The orders-service manages order lifecycle, tracking and fulfillment, calling
payments-service and emitting order events.

## Common incidents
- **Payment call failures** — check payments-service health and timeouts.
- **Event publish lag** — inspect the notification-worker consumer.
- **Database latency** — review the PostgreSQL `orders` database.

## Escalation
Page `commerce-oncall`; escalate to the commerce-team DRI.
