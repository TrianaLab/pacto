# Infrastructure contracts

**Problem.** Your platform offers a fixed set of infrastructure types — Postgres, Redis, object storage, secrets — provisioned by some declarative tool (Crossplane, Terraform, an internal operator). You want each infrastructure type to be self-describing, governed like services, and machine-readable by the tool that turns claims into real resources.

**Primitives.**

- **A pacto contract per infrastructure type**, published by the platform team
- **`policies[]`** carrying the platform rules (HA, backups, version floors)
- **`configurations[]`** carrying the provisioning schema (the team-controllable subset of fields)
- **`metadata.labels`** carrying provisioner hints — opaque to pacto, meaningful to your CI tool

**Example: a postgres infrastructure contract.**

```yaml
pactoVersion: "1.2"

service:
  name: postgres
  version: 17.0.0
  owner:
    team: platform

metadata:
  labels:
    platform/provisioner: crossplane
    platform/claim-kind: PostgreSQLClaim
    platform/claim-api-version: database.platform.example.com/v1alpha1

policies:
  - name: postgres-policy
    schema: policy/schema.json   # enforces version >= 17, backups enabled, HA in prod

configurations:
  - name: provisioning
    schema: configuration/schema.json   # derived from the provisioning claim's OpenAPI schema — the team-controllable subset of fields
```

**The provisioning schema** validates "did the team write a sensible claim?" — instances in range, valid size enum, schedule cron syntax, etc.

Pacto doesn't invent this schema — it's derived from the provisioning claim's own OpenAPI schema (a Crossplane XRD, a Terraform module's variables), the team-controllable subset of the claim. The configuration a team writes through the contract *is* the configuration that feeds the underlying claim — validated once, with no second definition to drift out of sync. The contract adds what the claim can't express on its own — an owner, a version, a policy and a stable ref other contracts depend on.

```json
{
  "type": "object",
  "properties": {
    "instances": { "type": "integer", "minimum": 1, "maximum": 5 },
    "size": { "type": "string", "enum": ["small", "medium", "large"] },
    "backups": {
      "type": "object",
      "properties": {
        "enabled": { "type": "boolean" },
        "schedule": { "type": "string" }
      }
    }
  }
}
```

**How services consume it.** A service contract references the infra contract as a configuration (see [pattern 3](composable-configs.md)). When CI generates deployment artifacts, it reads `metadata.labels` from the resolved infra contract to dispatch — no hardcoded mapping from "this configuration name" to "that claim kind". Adding a new infrastructure type is one new contract, not a code change in CI.

**Versioning the contract is versioning the platform interface.** A bump from `postgres:17.0.0` to `postgres:18.0.0` lets services migrate at their own pace by ref-pinning, and the policy can tighten with each major version (see [pattern 5](progressive-policy.md)).

**Cross-links:** [`metadata`](../contract-reference/sections.md#metadata) · [`policies`](../contract-reference/sections.md#policies) · [`configurations`](../contract-reference/sections.md#configurations)

---
