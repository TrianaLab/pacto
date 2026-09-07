# Payments Service v1.2.0

Adds optional fraud detection integration. When FRAUD_CHECK_ENABLED is true, charges are evaluated by fraud-service before processing.

## Changes from v1.1.0
- Added optional fraud-service dependency
- Added optional risk_score field in charge response and payment.completed event
- Added FRAUD_CHECK_ENABLED configuration
