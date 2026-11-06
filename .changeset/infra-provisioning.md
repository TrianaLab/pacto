---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Add `pactoVersion: "2.6"` with `service.lifecycle`, which deprecates a whole
service with a sunset and a successor, and with `values` and a `configuration`
selector beside a configuration `ref`. Every 2.5 contract is valid under 2.6. A
`"2.0"` to `"2.5"` contract that sets any of them reports `NEEDS_PACTO_2_6`.
Consumers of a deprecated service are warned with
`DEPENDENCY_SERVICE_DEPRECATED` or `DEPENDENCY_SERVICE_SUNSET`, and `pacto diff`
classifies the schedule like an interface lifecycle.

Configuration references are now resolved and their values validated against
the referenced contract's selected configuration schema in a new
`configuration-refs` validation stage, in the fleet, the delivery gate,
`pacto_check`, `pacto_edit` and the operator. `pacto validate`, `pacto push` and
`pacto publish` fail with `CONFIG_REF_UNRESOLVED` on an unreachable
configuration ref whose values or selection depend on it, like a policy ref; a
`"2.0"` to `"2.5"` ref with no values anywhere only warns. Environment values
of `"2.0"` to `"2.5"` ref configurations are now checked, and only 2.6 reports
missing required values for a ref configuration. A failed configuration-refs
stage does not skip environments. Impact, graph traversal and dependents count
resolved configuration-reference consumers, and impact checks their values
against the new provider schema. Neighborhood edges gain the
`configuration_reference` relation.

Provider adapters report provisioning status as `ResourceProvisioned`
evidence, written with `pacto evidence provisioned`. A trust entry needs
`allowedObservationKinds` listing `ResourceProvisioned` to report it. The
Evidence Server's targets endpoint answers `pacto.dev/evidence-source/v3` with
a `provisioning` list only when it holds provisioning reports, and v2
otherwise, and target-state files accept `pacto.dev/fleet-targets/v2` with
unsigned provisioning reports. Offering uses carry a provisioning status per
environment that reads Unknown without a current report. `pacto fleet
versions`, `pacto_fleet_versions` and the dashboard list each provider's
versions with their lifecycle and range status, `--consumes` and `consumes`
list an offering's consumers, and `pacto_edit` gains
`remove_configuration_refs`, `add_environments`, `set_environment_values` and
`lifecycle`. A provider with no Kubernetes target that declares its offering
is visible to every signed-in viewer, with its targets hidden and its hidden
consumers counted. `OfferingProvider.revisions` and `activeRevisions` become
`versions`.

The operator resolves configuration references with host-scoped credentials
and validates their values, `spec.overrides` and bound ConfigMaps against the
resolved schema. `status.metadata` renders non-string values as JSON.
