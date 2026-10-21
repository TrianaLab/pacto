<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from `go run ./integrations/kubernetes/cmd --help` (real output) + cmd/main.go.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Operator configuration

Controller flags and their exact defaults are captured from the operator's real `--help` output.

**One dash or two makes no difference.** The table shows the single-dash spelling because that is how Go's `flag` package prints and reports flags, and other pages write `--enable-metrics-observation`. The parser accepts both forms identically: `--enable-metrics-observation=x` and `-enable-metrics-observation=x` produce the same `invalid boolean value "x" for -enable-metrics-observation` error. Neither spelling is more correct.

**Two different defaults can apply to the same flag.** The Default column below is the *binary's* default -- what you get running the controller with no arguments. The Helm chart renders its own fixed argument list, so where a chart value exists it decides, and its default may differ. `-enable-dashboard` is the one that catches people out: the binary defaults it off, the chart's `dashboard.enabled` defaults it on, and a chart install therefore runs the managed dashboard. See the [Helm reference](helm-reference.md) for the values and their defaults.

**A flag the chart never renders cannot be set on the documented install path.** The chart has no `extraArgs`, so the *Via chart* column is the whole story: `chart value` means some value in `values.yaml` renders this flag; `always on` means the chart hardcodes it and no value changes it; `no` means the chart never passes it, and reaching it requires patching the Deployment after install -- which `helm upgrade` then reverts. See [Limitations](limitations.md#opt-in-features).

## Command-line flags

| Flag | Type | Default | Via chart | Description |
| --- | --- | --- | --- | --- |

## Environment variables

Read directly by the controller entrypoint (`cmd/main.go`), typically wired through the downward API in the chart's Deployment.

| Variable | Purpose |
| --- | --- |
| `OPERATOR_DEPLOYMENT_NAME` | Operator Deployment name, used to set ownerReferences on dashboard resources. |
| `POD_NAMESPACE` | Namespace the operator (and its managed dashboard) runs in. Required. |
