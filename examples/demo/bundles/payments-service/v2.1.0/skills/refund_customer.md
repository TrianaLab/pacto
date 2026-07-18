# Refund a customer

Domain workflow for issuing a refund. The `createRefund` tool wraps the raw
`POST /refunds` operation, but the business rules below are not expressed by the
OpenAPI contract and must be followed.

## Steps

1. Confirm the payment is refundable — call `getPaymentIntent` with the customer's
   `payment_intent_id`. Only intents in the `succeeded` state can be refunded.
2. Decide the amount:
   - **Full refund** — omit `amount`; the whole captured amount is returned.
   - **Partial refund** — set `amount` in the smallest currency unit (cents), never
     more than the captured amount.
3. Set `reason` to one of `duplicate`, `fraudulent`, or `requested_by_customer`.
   Use `fraudulent` only when fraud has been confirmed by the fraud-service — it
   affects the customer's risk profile.
4. Call `createRefund`. Refunds are idempotent per `payment_intent_id` + `amount`;
   do not blindly retry on timeout — re-check with `getPaymentIntent` first.

## Guardrails

- Never refund more than the original captured amount.
- A refund cannot be reversed once issued.
- Refunds are money-movement: prefer a partial refund you can repeat over a full
  refund you cannot undo.
