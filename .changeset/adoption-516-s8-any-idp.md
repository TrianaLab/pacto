---
"@pacto/core": minor
---

The served Backstage route `GET /api/backstage/entities` and `pacto export backstage` add four annotations to each Component: `pacto.trianalab.io/dependency-ranges` with each dependency's accepted range and range status, `pacto.trianalab.io/conformance` with the service's runtime conformance in the reader's view, `pacto.trianalab.io/offering` with the platform offering it provides and `pacto.trianalab.io/contributed-by` with the producer of a contributed record. A contributed record never reads declared: it reads inferred, or observed when a running target shows it, and a stale one reads partial. The new [Sit on top of any IDP](https://pacto.run/latest/any-idp/) guide shows a private IDP that contributes a service, reports targets and provisioning, and reads ranges and conformance back, with only `curl` and the `pacto` CLI. No contract field, `pactoVersion`, route, MCP tool, Helm value or CRD changes.
