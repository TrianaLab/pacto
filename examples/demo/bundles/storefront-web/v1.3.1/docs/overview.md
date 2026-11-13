# storefront-web

1.3.1 retires pr-482 now that pull request 482 has merged, and declares the other environments as 1.3.0 does.

1.3.0 says how long each environment lives: staging and production are persistent and storefront-qa owns staging, preview is temporary and expired on 2026-09-30, and pr-482 is the preview for pull request 482, on production's values with a single replica and its own ingress host. production still sets no ingress host, so its values still fail the chart schema.

1.2.1 declares the commerce business domain (pactoVersion 2.4) and pins platform-app-config to 1.0.1. Pacto fetches that bundle and checks the preview values for platform against its schema; when it cannot fetch the bundle, validation fails.

1.2.0 updates the chart, whose schema now requires ingress.host. production sets only a class name, so its values fail the schema and Pacto points at the line in environments/production.yaml.

This service is part of the synthetic demo fleet: its contract is made up.
