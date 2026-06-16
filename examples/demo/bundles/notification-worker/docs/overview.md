# Notification Worker

Background worker that consumes notification events and dispatches emails via the external email provider. Runs as a scheduled workload with configurable concurrency and retry policies.

## Interfaces
- **Events** (internal) - Consumes notification requests, publishes delivery status

## Dependencies
- **email-provider** - External email delivery (SendGrid)

## Workload
Scheduled worker with dead letter queue support.
