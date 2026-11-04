# storefront-web

1.2.1 declares the commerce business domain (pactoVersion 2.4) and changes nothing else.

1.2.0 updates the chart, whose schema now requires ingress.host. production sets only a class name, so its values fail the schema and Pacto points at the line in environments/production.yaml.

This service is part of the synthetic demo fleet: its contract is made up.
