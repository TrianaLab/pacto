# storefront-web

1.2.1 declares the commerce business domain (pactoVersion 2.4) and pins platform-app-config to 1.0.1. Pacto fetches that bundle and checks the preview values for platform against its schema; when it cannot fetch the bundle, validation fails.

1.2.0 updates the chart, whose schema now requires ingress.host. production sets only a class name, so its values fail the schema and Pacto points at the line in environments/production.yaml.

This service is part of the synthetic demo fleet: its contract is made up.
