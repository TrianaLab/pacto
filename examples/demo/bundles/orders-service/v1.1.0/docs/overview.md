# Orders Service v1.1.0

Adds order tracking and shipping events. notification-worker dependency becomes required.

## Changes from v1.0.0
- Added `GET /orders/{id}/tracking` endpoint
- Added `order.shipped` event
- Added `shipping_address` field to order creation
- `notification-worker` dependency is now required
- Added `ENABLE_ORDER_TRACKING` config
