---
"@pacto/core": minor
---

The dashboard serves the fleet as Backstage catalog entities at
`GET /api/backstage/entities`: the same multi-document YAML as
`pacto export backstage`, narrowed to what the reader may read. A source that
is unavailable or missing records answers 503, never a shorter catalog, so
Backstage keeps the entities it had.

On a dashboard with sign-in, a server-side reader sends a Kubernetes token as
`Authorization: Bearer` on GET requests under `/api/backstage/`; the token is
reviewed like a pasted one and no session is created. The OpenAPI document and
the generated SDK do not change. The WASM demo serves the route offline and,
because its fleet holds an unreachable source, answers it with that 503.
