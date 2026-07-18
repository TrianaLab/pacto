# Platform-published policy + schema contract

**Problem.** As a platform team you want to enforce contract structure rules ("every service must declare an owner and a health endpoint") *and* publish the schema that validates deployment values for your standard chart. You want both to live in one versioned artifact that every service references — so updates propagate via a version bump, not a wiki announcement.

**Primitives.**

- **One contract** carrying both `policies[].schema` (the rules contract authors must follow) and `configurations[].schema` (the values shape the chart accepts)
- Service contracts reference it for either or both via `policies[].ref` and `configurations[].ref`

**The platform contract.**

```yaml
# pactos/platform-service/pacto.yaml
pactoVersion: "1.2"

service:
  name: platform-service
  version: 2.0.0
  owner:
    team: platform

policies:
  - name: platform-policy
    schema: policy/schema.json     # requires owner, runtime.health, runtime.workload

configurations:
  - name: deployment
    schema: configuration/schema.json   # the standard chart's values.schema.json
```

**A service references both.**

```yaml
# pactos/my-service-api/pacto.yaml
policies:
  - name: platform-policy
    ref: oci://ghcr.io/example/pactos/platform-service:2.0.0

configurations:
  - name: deployment
    ref: oci://ghcr.io/example/pactos/platform-service:2.0.0
```

**Mix and match.** Teams using the platform's standard chart reference both. Teams that ship their own chart (a third-party Keycloak chart, a custom operator) still reference the policy — contract structure rules are universal — but vendor their own configuration schema locally. The moment a team needs inline or override `values`, it must vendor the schema: a `ref`-ed config is schema-only (see [Configuration Schema Ownership Models](../contract-reference/sections.md#configuration-schema-ownership-models)).

```yaml
policies:
  - name: platform-policy
    ref: oci://ghcr.io/example/pactos/platform-service:2.0.0

configurations:
  - name: deployment
    schema: configuration/values.schema.json   # vendored from their chart
```

**One bundle, versioned.** The same JSON Schema validates the contract at CI time *and* the chart values at install time.

**Cross-links:** [Configuration Schema Ownership Models](../contract-reference/sections.md#configuration-schema-ownership-models) · [`policies`](../contract-reference/sections.md#policies) · [Policy as a contract](../platform-engineers.md#policy-enforcing-contract-standards)

---
