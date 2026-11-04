# Cancel an order

Domain workflow for cancelling a customer order. The interface exposes the
operations; the sequencing and rules below are the business knowledge an agent
cannot infer from the OpenAPI contract alone.

## Steps

1. Look up the order with `getOrder` to confirm it exists and read its status.
2. Check fulfilment with `getOrderTracking`. An order that has already shipped
   cannot be cancelled — direct the customer to the returns flow instead.
3. If the order is still `pending` or `confirmed`, call `cancelOrder`.
4. If the order was already paid, cancelling does **not** refund automatically —
   follow the payments-service "Refund a customer" skill to return the funds.

## Guardrails

- Never cancel an order that is `shipped` or `delivered`.
- Cancelling a paid order is a two-step process: cancel, then refund.
