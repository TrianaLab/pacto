---
"@pacto/core": minor
"@pacto/operator-chart": minor
---

Detect what the cluster provides. On start and every 5 minutes the operator writes the registry most namespaces pull from, the cloud, a sign-in provider, an exposure controller with a `--set` line for the dashboard route, whether the Prometheus Operator is served and the node kernels with BTF likely, unlikely or unknown to one ConfigMap, `<release>-detected`, in its namespace. Nothing is turned on. The rows show as inferred under Data sources, in `helm upgrade` notes, in `pacto fleet status` and `pacto fleet providers` with `--detected`, in `pacto_fleet_status` with `pacto mcp --fleet --detected` and in `pacto dashboard` with `PACTO_DASHBOARD_DETECTED`. A missing or stale detection names itself and never makes an answer partial. The chart grants `nodes list` in the cluster-wide ClusterRole and lets the operator create that ConfigMap and get and patch only it. With `controller.watchNamespaces` the registry row is partial, cloud and nodes are unknown and no ClusterRole is added.
