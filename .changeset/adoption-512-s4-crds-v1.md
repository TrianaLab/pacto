---
"@pacto/k8s-module": major
"@pacto/core": minor
---

Graduate the Pacto CRDs to v1. All six CRDs (pactos, pactorevisions and noderuntimeobservations in pacto.trianalab.io; stages, operations and approvals in delivery.pacto.trianalab.io) store v1 and keep serving v1alpha1 with a deprecation warning until chart 8.0. On startup the operator and the delivery promoter rewrite every stored object as v1 and set the CRD's storedVersions to [v1], retrying until the new CRDs are served; the log line `storage migration: <crd> rewrote N objects as v1` reports it. On helm install and upgrade the chart refuses to render a CRD that drops a version the cluster still stores. The operator and the promoter need get on their CRDs and patch on their status, scoped by name. The Go module path is github.com/trianalab/pacto/integrations/kubernetes/v7; types live in api/v1 and api/delivery/v1. The CLI and dashboard default to v1 and read the version a cluster serves, so they keep working with chart 6; manifests that pacto init --delivery writes need chart 7. The pkg/delivery wire types are covered by semantic versioning. See MIGRATION.md.
