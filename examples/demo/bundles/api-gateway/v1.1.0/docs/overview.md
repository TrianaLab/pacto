# API Gateway v1.1.0

Adds WebSocket support for real-time order tracking updates and order tracking endpoint.

## Changes from v1.0.0
- Added GET /api/v1/orders/{id}/tracking endpoint
- Added WebSocket endpoint /ws/orders/{id}/updates
- Added ENABLE_WEBSOCKETS configuration
