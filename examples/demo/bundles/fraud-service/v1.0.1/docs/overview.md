# Fraud Service

gRPC-based real-time fraud detection service. Evaluates transactions using ML-based risk scoring and caches feature data in Redis.

## Interfaces
- **gRPC** on port 50052 (internal)

## Dependencies
- **redis** - Feature caching for ML model
