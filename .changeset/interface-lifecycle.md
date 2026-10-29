---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Add `pactoVersion: "2.2"` and an interface lifecycle, so a provider can retire
an interface on a schedule its consumers can see. Every 2.1 contract is valid
under 2.2, which also carries an interface's `compatibilityEpoch` and
`guarantees` and a dependency's `requires`. A 2.2 contract needs Pacto core
4.3.0 or later and operator 6.3.0 or later. A `"2.0"` or `"2.1"` contract that
uses a 2.2 field reports `NEEDS_PACTO_2_2` for each.

```yaml
pactoVersion: "2.2"
interfaces:
  - name: orders-v1
    type: openapi
    ref: interfaces/orders-v1.yaml
    lifecycle:
      deprecated: "2026-10-01"
      sunset: "2027-04-01"
      successor: orders-v2
```

- `pacto validate` checks that both dates are `YYYY-MM-DD`, that the sunset is
  not before the deprecation (`INVALID_INTERFACE_LIFECYCLE`) and that the
  successor names another interface or is an absolute URL
  (`INTERFACE_SUCCESSOR_UNKNOWN`). A 2.0 or 2.1 contract with a lifecycle
  reports `NEEDS_PACTO_2_2`.
- `pacto validate --closure` warns a consumer about each deprecated interface
  of a service it depends on directly, naming the interface, its sunset and its
  successor (`DEPENDENCY_INTERFACE_DEPRECATED`). From the sunset date the code
  is `DEPENDENCY_INTERFACE_SUNSET`.
- `pacto diff` reports deprecating an interface as non-breaking and moving its
  sunset earlier as potentially breaking. Removing an interface before its
  sunset stays breaking, and the reason says so.
- `pacto explain`, the MCP contract summary and the dashboard API show an
  interface's lifecycle.
