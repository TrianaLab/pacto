# http-service

http-service is a golden path: a starting point for a new HTTP API on the platform runtime. Its contract declares `service.template`, so the fleet lists it under Golden paths and never counts it as a running service or as a consumer of what it requires.

A service created from it gets:

- An internal OpenAPI interface with a health operation to replace with its own.
- A dependency on postgresql `^16.0.0` and a request for a small PostgreSQL database with 20 GiB of storage through the `provisioning` configuration of postgresql 16.1.0.
- The `http-standards` policy, which asks for an owner team, a tier and a criticality.
- Health and metrics capabilities and a stateless workload.
- Two optional plugins: `dockerfile` writes a Dockerfile and `github-actions` writes a CI workflow. Pacto lists them and runs them only when you name them with `--run-plugins`.

Create a service from it with:

```bash
pacto init gift-cards --from oci://ghcr.io/trianalab/pacto/http-service:1.0.0 --owner checkout --domain commerce
```

Add `--plan` to see every step first. The created contract records `service.origin`, so the fleet links the new service back to this path.

This service is part of the synthetic demo fleet: its contract is made up.
