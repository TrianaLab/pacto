# Delivery and dashboard performance against a real apiserver

`make perf-envtest` measures the dashboard's delivery and fleet views and the
delivery promoter against a real kube-apiserver and etcd: the binaries
envtest uses, fetched by `make -C integrations/kubernetes setup-envtest`.
Nothing is faked between the handler and the apiserver.

| test | where | build tag |
|---|---|---|
| `TestPerfEnvtest` (dashboard) | `internal/cli/perf_envtest_test.go` | `perfenvtest` |
| `TestSharedReadsRBACIsolation` (dashboard) | `internal/cli/shared_envtest_test.go` | `perfenvtest` |
| `TestPerfPromoter` (promoter) | `integrations/kubernetes/internal/delivery/perf_envtest_test.go` | `perfenvtest` |

## Run it

```sh
make perf-envtest                          # both, with the budget assertions
PERF_REPORT=/tmp/perf.md make perf-envtest # also writes the tables
```

One test at a time, with assets you already have:

```sh
export KUBEBUILDER_ASSETS=$(make -s -C integrations/kubernetes setup-envtest | tail -n 1)
PERF_QUICK=1 go test -tags perfenvtest -run TestPerfEnvtest -v ./internal/cli/     # 500 Stages only
PERF_VIEWS=inbox,history go test -tags perfenvtest -run TestPerfEnvtest -v ./internal/cli/
PERF_CPUPROFILE=/tmp/cpu.prof go test -tags perfenvtest -run TestPerfEnvtest -v ./internal/cli/
cd integrations/kubernetes && go test -tags perfenvtest -run TestPerfPromoter -v ./internal/delivery/
```

The full dashboard run takes about 12 minutes; the promoter about 2.

## What it builds

- 60 namespaces, 500 services in 4 environments: 2,000 Stages, each with its
  apply OCIRepository. 10,000 Operations: 85% Succeeded, 5% Failed, 2%
  Errored, 5% Held for a team, 3% in flight, a tenth of them on one hot Stage,
  and an Approval on every tenth concluded one. The first pass runs at a
  quarter of that (500 Stages, 2,000 Operations).
- Three people, authenticated with bearer tokens shaped like the ID tokens the
  dashboard passes through, so the apiserver and the dashboard name the same
  person: alice reads 3 namespaces, bob 40 and audrey (an auditor, read-only)
  all of them. 49 more auditors make the concurrent runs 50 distinct people,
  whom no per-person cache helps.
- The dashboard runs as `pacto dashboard` does in shared mode (its own
  `dashboardIdentity`, `dashDelivery`, the shared cache of Stages and
  Operations, the RBAC viewer with SubjectAccessReview) on loopback, with its
  own identity holding the rights the operator grants its ServiceAccount; only
  the OIDC session is replaced by the bearer token.

`TestSharedReadsRBACIsolation` checks on a real apiserver that the shared cache
shows a person only the namespaces their RBAC allows, that a token is the
person the apiserver says it is (whatever its claims say), that the cache
answered every read, and that a removed RoleBinding hides its namespace.

Each view is warmed once (the cold column), then served from 1, 10 and 50
callers. Apiserver requests are counted beneath the dashboard's own cache, so
an answer it served from memory is not counted. Allocations are the whole
process (dashboard and test client).

## Budgets

The tests fail when a request fails, when the auditors' apiserver requests per
dashboard request exceed the budget, or when the p95 at 10 callers does; the
promoter when a promote costs more than 25 apiserver requests or the burst of
200 takes longer than 4 minutes. The budgets are about twice the apiserver
counts and five times the latencies below, which a heavily loaded laptop
measured.

## The shared cache

Full size, the same laptop and load for both columns. Before reads every list
with each person's token (the fixes below); after reads Stages and Operations
from the dashboard's shared cache, filtered per person with a
SubjectAccessReview kept 10 seconds.

| view | who | callers | p50 before | p50 after | p95 before | p95 after | apiserver req/request before | after | allocations per request before | after |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| inbox | 1 auditor | 1 | 462ms | 85ms | 506ms | 92ms | 4.0 | 3.2 | 2.7M | 472k |
| inbox | 10 auditors | 10 | 1.1s | 164ms | 1.1s | 195ms | 2.9 | 2.0 | 2.7M | 471k |
| inbox | 50 auditors | 50 | 11.6s | 821ms | 13.3s | 1.0s | 3.7 | 3.2 | 2.7M | 472k |
| environments | 10 auditors | 10 | 858ms | 156ms | 917ms | 164ms | 2.8 | 0.5 | 2.7M | 468k |
| environments | 50 auditors | 50 | 9.7s | 659ms | 12.4s | 783ms | 3.6 | 0.8 | 2.7M | 468k |
| history, all Stages | 10 auditors | 10 | 1.4s | 220ms | 1.4s | 231ms | 41.4 | 1.0 | 3.3M | 668k |
| history, all Stages | 50 auditors | 50 | 12.3s | 1.0s | 14.7s | 1.1s | 44.7 | 1.6 | 3.3M | 668k |
| history, hot Stage | 50 auditors | 50 | 1.4s | 128ms | 1.6s | 177ms | 1.6 | 0.0 | 512k | 73k |
| inbox | bob | 1 | 40ms | 9ms | 44ms | 17ms | 1.0 | 0.0 | 1.9M | 332k |

The apiserver requests left are each new person's first questions: who their
token is, the published identity policy and whether they may list Stages and
Operations everywhere. The cache itself costs one list and one watch of each
kind for the life of the dashboard.

## Before and after

Full size (2,000 Stages, 10,000 Operations), before the shared cache. Before
is without the fixes below; after is with them.

| view | who | callers | p50 before | p50 after | p95 before | p95 after | apiserver req/request before | after |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| inbox | alice | 1 | 555ms | 37ms | 565ms | 43ms | 9.0 | 1.0 |
| inbox | bob | 1 | 14.7s | 235ms | 24.9s | 317ms | 95.2 | 1.0 |
| inbox | 1 auditor | 1 | 5.2s | 1.6s | 6.4s | 1.9s | 4.8 | 4.2 |
| inbox | 10 auditors | 10 | 21.9s | 3.8s | 22.1s | 3.9s | 4.5 | 4.2 |
| inbox | 50 auditors | 50 | 2m37s | 33.8s | 2m39s | 37.8s | 5.0 | 4.7 |
| environments | alice | 1 | 1.7s | 48ms | 12.7s | 127ms | 21.2 | 2.6 |
| environments | bob | 1 | 14.7s | 202ms | 24.2s | 291ms | 95.2 | 1.4 |
| environments | 10 auditors | 10 | 20.7s | 7.9s | 20.9s | 8.2s | 4.0 | 4.0 |
| environments | 50 auditors | 50 | 4m7s | 33.7s | 4m10s | 34.3s | 4.0 | 3.9 |
| environments, filtered | 10 auditors | 10 | 25.5s | 4.3s | 25.8s | 4.3s | 4.0 | 4.0 |
| environments, filtered | 50 auditors | 50 | 4m15s | 34.8s | 4m22s | 35.1s | 4.0 | 4.0 |
| history, all Stages | alice | 1 | 9.6s | 192ms | 20.5s | 668ms | 69.2 | 11.4 |
| history, all Stages | bob | 1 | 25.0s | 313ms | 34.6s | 313ms | 143.2 | 0.2 |
| history, all Stages | 1 auditor | 1 | 17.3s | 905ms | 18.5s | 1.2s | 52.0 | 41.6 |
| history, all Stages | 10 auditors | 10 | 1m32s | 3.5s | 1m33s | 3.6s | 52.0 | 51.7 |
| history, all Stages | 50 auditors | 50 | timed out (5m) | 57.7s | timed out (5m) | 57.9s | 52.0 | 52.0 |
| history, hot Stage (1,000 ops) | 10 auditors | 10 | 1.2s | 900ms | 1.2s | 934ms | 3.0 | 1.4 |
| published | bob | 1 | 6.2s | 115ms | 30.6s | 625ms | 77.6 | 0.8 |
| published | 50 auditors | 50 | 30.8s | 16.9s | 32.0s | 17.5s | 1.0 | 0.8 |
| autonomy | bob | 1 | 6.2s | 89ms | 6.2s | 133ms | 41.0 | 0.0 |
| autonomy | 10 auditors | 10 | 4.0s | 1.7s | 4.1s | 1.8s | 1.5 | 1.2 |
| fleet overview (SAR view) | 50 auditors | 50 | 100ms | 41ms | 126ms | 43ms | 0.0 | 0.0 |

How one auditor's request grows with the cluster:

| view | p50, 500 Stages / 2k ops (before / after) | p50, 2,000 Stages / 10k ops (before / after) | allocations per request at 2,000 (before / after) |
|---|---:|---:|---:|
| inbox | 867ms / 451ms | 5.2s / 1.6s | 9.3M / 2.7M |
| environments | 877ms / 323ms | 5.7s / 1.8s | 9.3M / 2.7M |
| history, all Stages | 8.5s / 383ms | 17.3s / 905ms | 28.6M / 3.3M |
| history, hot Stage | 74ms / 78ms | 174ms / 519ms | 776k / 512k |
| published | 244ms / 219ms | 746ms / 618ms | 2.6M / 2.0M |
| autonomy | 251ms / 288ms | 984ms / 547ms | 2.6M / 2.0M |
| fleet overview | 3ms / 2ms | 192ms / 7ms | 7k / 7k |

The fleet views read no Kubernetes object per request: the snapshot is built
in the background, and the per-person view costs one SubjectAccessReview for a
cluster-wide reader and one per namespace otherwise (61 for alice and bob),
each kept a minute.

The promoter, 200 promotes at once on 200 Stages, 4 workers and a 3s operation
poll, as `cmd/delivery` runs it. Before: every Stage polled every 3s through
client-go's 20/30 QPS. After: Stages wake on watch events with a 1m resync,
through a 50/100 QPS client:

| | promotes | all moved | time to move p50 | p95 | max queue depth | apiserver req/operation | idle apiserver req/s |
|---|---:|---:|---:|---:|---:|---:|---:|
| before | 200 | 1m15s | 53.9s | 1m12s | 200 | 11.1 | 30.6 |
| after | 200 | 13.3s | 8.8s | 12.6s | 200 | 11.2 | 0.0 |

## The fixes

Each was found here, and its effect is in the table above.

1. **One request's reads were throttled to 5 per second.** The dashboard
   builds a client per request with the caller's token and client-go's default
   limiter (5/s, bursting to 10), so a history page (two lists, then a Stage
   and its Operations for each of 25 Stages) waited 8.4s on itself. The
   per-caller client now allows 100/s; the apiserver's priority and fairness
   protects the cluster. History, all Stages, one auditor: 8.5s to 0.4s at the
   small size.
2. **A person who cannot list cluster-wide waited on one namespace after
   another.** bob's 40 namespaces were 80 lists in a row. They now run eight
   at a time. bob's inbox: 14.7s to 0.2s (with fix 1).
3. **Every view listed every Operation.** The views read only held, errored
   and failed ones, and a Stage's history only that Stage's. The Operation CRD
   now declares `spec.stage` and `status.outcome` selectable fields and the
   client asks for `status.outcome!=Succeeded` or `spec.stage=<name>`. A
   cluster whose CRD predates the fields (or Kubernetes 1.30, where they are
   alpha) refuses the selector; the client then lists everything and filters,
   so the answer is the same. 10,000 Operations become the 1,500 that did not
   succeed.
4. **A person's views each listed the same objects again.** Every caller's
   GETs are kept two seconds, keyed by the exact URL and the credentials sent,
   so one person's answer is never served to another; concurrent identical
   reads share one request, and a write through the dashboard forgets the
   writer's reads at once. A SelfSubjectReview is not a write. alice polling
   her inbox: 9 apiserver requests per view to 1.
5. **Listed objects were decoded three times.** client-go decodes the list;
   the client wrote it back to JSON and decoded it again into the typed
   struct. It now converts the decoded objects directly: 2.1 times faster and
   3.7 times fewer bytes on a list of 2,000 Stages.
6. **The promoter's idle polling ate its whole rate budget.** Every Stage was
   reconciled every 3s and read its OCIRepositories and Kustomizations from
   the apiserver: 30 requests per second at rest with 200 Stages, above the 20
   QPS client limit, so a burst of promotes queued behind it. A Stage now
   reconciles when one of its operations, its upstream Stage, its
   OCIRepositories, the Kustomizations that apply them or its Application
   changes, and every minute (`--resync`) for what no watch covers. Those
   reads come from the watch cache, except where the promoter decides on the
   apply OCIRepository's digest: the foreign-writer check, the ship path, a
   witnessed override and what an observing Stage verifies read it from the
   apiserver, so a lagging cache never freezes a Stage or records a change
   that did not happen. The client allows 50 requests per second, bursting to
   100 (`--kube-api-qps`, `--kube-api-burst`). The burst: all moved in 13s
   instead of 1m15s.

## What is left

- **Operations still poll every 3s while they wait on Flux.** Waking them on
  Kustomization events as well would remove that poll if it becomes a cost.
