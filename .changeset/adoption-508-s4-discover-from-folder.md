---
"@pacto/core": minor
---

Pacto reads the files a service folder already has into the inferred record:
OpenAPI, AsyncAPI and gRPC files at any path, Helm charts read statically with
their Chart.lock, Kustomize, Compose, CODEOWNERS, catalog-info.yaml,
environment overlays and values files, and Argo CD and Flux objects. Each value
names its file and line and stays out of pacto.yaml until a person declares it.
An owner read from CODEOWNERS or catalog-info.yaml is shown as attested and
never clears a gate. Every file Pacto recognizes and cannot read is a named
finding in pacto init, pacto explain, MCP and the dashboard.
