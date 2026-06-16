# Pacto Demo

Single entry point for the entire e-commerce platform graph. Depends only on `frontend` - all other services are discovered recursively through the dependency chain.

## Usage

```
pacto dashboard oci://ghcr.io/trianalab/pacto-demo/pacto-demo
```

This reveals the full platform: frontend -> api-gateway -> [auth, orders, payments] -> [postgresql, redis, stripe, fraud, notifications] -> [email-provider].
