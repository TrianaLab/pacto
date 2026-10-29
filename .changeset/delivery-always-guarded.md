---
"@pacto/k8s-module": minor
---

Delivery always installs its admission policies, and a promoter started
without `--admission-policies` admits no operation and counts no approval: its
Operations and Stages show `Unguarded`.

Breaking for delivery (alpha): the chart removes
`delivery.admissionPolicies.enabled` and
`delivery.admissionPolicies.allowUnguarded`, and fails to render while either
key is set. Delete both from your values. To render offline for `helm lint`
or a `helm template` without `--api-versions`, set
`delivery.admissionPolicies.skipCapabilityCheck=true`, which skips only the
Kubernetes 1.30 and ValidatingAdmissionPolicy API check. An upgrade with
`--reuse-values` carries the old `enabled: true` over, so use
`--reset-then-reuse-values`.
