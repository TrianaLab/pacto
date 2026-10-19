---
"@pacto/core": major
---

Judge more of the contract at runtime, without turning an inability to observe
into a violation.

Evidence payloads gain facts a producer may report: `InterfaceObservation`
carries `protocol` (`http1`, `http2`, `tls`, `other`) and `exposure`
(`external`, `cluster`), `DependencyObservation` carries the provider's
`version` and `undeclared` and `PersistenceObservation` carries `shared`. All
are `omitempty`, so a producer that does not set them emits the same JSON as
before.

`validation.Evaluate` checks them as facets of an observed assertion:

- `INTERFACE_PROTOCOL_MISMATCH` — `openapi` answering neither HTTP/1 nor HTTP/2,
  or `grpc` answering anything but HTTP/2.
- `INTERFACE_VISIBILITY_MISMATCH` — an `internal` interface (the schema default,
  now applied when `visibility` is omitted) exposed outside the cluster.
- `DEPENDENCY_VERSION_MISMATCH` — the provider's version outside
  `compatibility`; an error for a required dependency, a warning for an
  optional one.
- `PERSISTENCE_MISMATCH` now also covers `local` scope with storage shared
  between replicas.
- `DEPENDENCY_UNDECLARED` — a reachable peer no dependency declares; always a
  warning, because a contract does not claim its dependency list is complete.

A facet with no usable fact is listed in the new `Coverage.Unverified` and never
changes the status. Every finding's `EvidenceRef` now names the `collector` and
`detectedAt` of the observation it came from.

`pkg/validation/schema/field-coverage.yaml` inventories every schema path and
runtime-relevant enum value with its runtime class, evidence, rule, outcomes,
limitations and tests; a test fails when the schema and the inventory disagree.
Every runtime-checked field also says how far each way of running runtime
conformance verifies it (`platforms`: `kubernetes`, `agent`, `run`, each
`verified`, `partial` or `unverified` with a reason and its tests).

New `pacto agent` command (Linux): a host daemon that watches TCP listeners and
connection outcomes with eBPF and attributes each socket to the workload whose
cgroup created it (a Kubernetes pod, a container, a systemd unit or a
`pacto-run-<n>` session), falling back to its network namespace when the cgroup
names none. `--sink stdout` prints one JSON snapshot per line; `--sink
kubernetes` writes the node's `NodeRuntimeObservation` and is what the
operator's node agent DaemonSet runs. Attribution reads the socket's cgroup id
in the kernel, so on cgroup v2 workloads that share the host's network (systemd
units, host-network containers) are told apart too.

`--sink local` and `--sink evidence` check workloads against their contracts on
the host itself. Workloads come from `--bindings` (a YAML list of `match`
(`unit`, `container`, `cgroup`, `session` or `pid`), `contract`, `host`,
`interfaces` and `dependencies`) and, with `--docker`, from running containers
labelled `dev.pacto.contract`, `dev.pacto.bind.<interface>` and
`dev.pacto.dep.<dependency>`. Interfaces and dependencies are judged from the
kernel, health and metrics capabilities and (with `--enable-probing`) the
interface protocol by probing the bound port. `local` prints every workload's
compliance each round; `evidence` signs each workload's evidence and sends it
to an Evidence Server, which needs contracts pinned by digest. Exposure,
provider version and persistence scope have no source off Kubernetes and stay
unverified; a declared `workload`, persistent durability or required
configuration reads `EVIDENCE_MISSING`. A workload that reaches another
checked workload its contract does not declare reports `DEPENDENCY_UNDECLARED`
named by that workload's contract service, with the operator's suppression
rule. A bindings-file workload starts with its oldest process, so earlier
connections are ignored. An agent off the host's network reports probes of
loopback-bound workloads unsupported, with a note, instead of probing its own
loopback.

New `pacto run --contract <dir|oci ref> [--bind iface=port] [--dep name=host:port]
-- command`: runs one command and checks its process tree against the contract
while it runs, printing a round every `--interval` and a final one after the
command exits, then exits with the command's exit code (`--fail-on
noncompliant|unknown` turns a bad final verdict into exit 1). With the
privileges for it on Linux the command starts in its own cgroup and is observed
with eBPF (collector `pacto-run+ebpf`); otherwise it falls back to polling
`/proc`, says so, and never reports a dependency unreachable because it cannot
see a failed connection (collector `pacto-run`); it falls back the same way
when the program attached but the command cannot start in the cgroup. The
command runs in a process group of its own: on a terminal pacto run owns, the
group takes the terminal over, otherwise SIGINT and SIGTERM are passed on to the
whole group, so a Ctrl-C reaches it once. Elsewhere the command runs
unobserved and every runtime assertion is unsupported. The command's stdin,
stdout and stderr stay its own: rounds go to stderr, or with `--report <file>`
to a file, one whole round per write. `--evidence-*` signs and sends every
round to an Evidence Server.

New public packages: `pkg/runtimefacts`, the platform-neutral runtime snapshot,
workload bindings and the verdicts that turn them into observations (the
operator now uses them too), and `pkg/probe`, the SSRF-safe HTTP health,
metrics and protocol probes, moved out of the operator.
