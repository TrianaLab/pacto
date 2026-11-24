---
"@pacto/core": minor
---

Ask which consumers a change or an outage of an external system affects, even
one nobody in your organization controls. `pacto impact --changed
external:<identity>` and `--unavailable external:<identity>` list every
service that declares, calls or uses the system, each with where the use was
seen, the client major its repository manifest names (always possible) and the
range it pins. A change puts every consumer in attention; an outage breaks a
declared required use, may degrade a declared optional one and is unknown for
the rest. The gate is always unverified (exit 2). When a consumer pins no
range, the answer ends with the dependencies block that pins one. The
`pacto_impact` MCP tool takes `external`, `POST /api/fleet/impact` takes
`externalKey` and the External page opens Change analysis with **What if it
changes?**. An external catalog entry can link the vendor's OpenAPI document
by digest (`openapi`); Pacto never fetches it. `pacto fleet publish-inferred
--external-catalog` records the link in the inferred contract and publishes a
new revision when its digest moves, and prints the impact question as the next
step. The provider of an external record carries `change`, what moved between
its two newest revisions, and a checked change of the OpenAPI digest or the
clients raises `EXTERNAL_CHANGED` in the knowledge category for 7 days. The
OCI fleet source reads the two newest revisions of an inferred contract.
