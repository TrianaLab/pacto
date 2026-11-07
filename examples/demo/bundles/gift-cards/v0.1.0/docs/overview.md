# gift-cards

gift-cards 0.1.0 is the checkout team's new service, created from the http-service golden path with:

```bash
pacto init gift-cards --from oci://ghcr.io/trianalab/pacto/http-service:1.0.0 --owner checkout --domain commerce
```

Everything in this contract came from the path: the internal API, the postgresql dependency, the request for a small PostgreSQL database, the `http-standards` policy and the health and metrics capabilities. `service.origin` records the path it was created from and that path's content digest, so the fleet links gift-cards back to http-service. The origin is declared: Pacto shows it as the service's own statement. The committed `pacto.lock` pins which postgresql revision the configuration reference resolves to.

This service is part of the synthetic demo fleet: its contract is made up.
