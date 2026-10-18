# Runtime conformance

A contract says what a service does: which ports it serves, which services it
calls. Runtime conformance checks that the running service really does it.
Pacto watches the service's network activity on the machine it runs on and
compares it with the contract. It runs anywhere Linux runs: a laptop, a CI job,
a Docker host, a bare-metal server or a Kubernetes cluster.

## Why it matters

The usual health signals miss the failures that hurt most:

- A process passes its health check, but nothing listens on the port its
  contract declares, so every caller gets "connection refused".
- A service keeps failing to reach a dependency it declared, and nobody notices
  until a user does.

Pacto turns each of these into a named finding against the contract, such as
`INTERFACE_ABSENT` or `DEPENDENCY_UNREACHABLE`, the same finding codes the
operator and the dashboard report.

## A one-minute example

Run your service through `pacto run` and tell it which port serves which
interface:

```bash
pacto run --contract ./payments --bind api=8080 -- ./payments-server
```

If `payments-server` starts but never opens port 8080, the final report says:

```text
session/pacto-run-4121  NonCompliant  1/1 evaluated  ./payments
  - [error] INTERFACE_ABSENT interface/api: interface "api" is not being served (pacto-run+ebpf)
```

No code changes, no SDK, no sidecar. [Run a command under Pacto](runtime-run.md)
has the full guide.

## What Pacto watches

Pacto answers three questions about each service, and nothing more:

- **Who listens?** Which TCP ports the service has open for callers.
- **Who connects?** Which addresses the service dials.
- **Did it work?** Whether each of those connections succeeded or failed.

It never reads message contents, request bodies, files, environment variables
or secrets. A listener is recorded as an address, a port and a process name; a
connection as a peer address and port, with counts and timestamps.

The watching is done with *eBPF*: a small program the Linux kernel runs safely
each time a TCP connection changes state. It sees connections open, close and
fail, not the requests sent over them.

## Kept, broken or don't know

Every runtime check ends in one of three answers, and Pacto never guesses
between them:

| Answer | Contract status | Example |
| --- | --- | --- |
| **Kept** | `Compliant` | the declared port is open and the declared dependency answered |
| **Broken** | `NonCompliant` | nothing listens on the declared port, or every recent connection to a dependency failed |
| **Don't know** | `Unknown` | the observer stopped reporting, or could not see enough to decide |

Silence is never a violation. If Pacto cannot see, it says "don't know" and
names why (for example `EVIDENCE_STALE` or `COLLECTION_FAILED`). A negative must
also last for a *stabilization window* (two minutes by default) before it is
reported, so a service that is still starting is not called broken.

## Three ways to run it

All three run the same watcher and the same evaluation; they differ in what
they watch and where the answer goes.

| | `pacto run` | `pacto agent` | Kubernetes node agent |
| --- | --- | --- | --- |
| What it watches | one command and its child processes | every workload on one host: containers, systemd units, processes | every pod on every node |
| How you name the workload | `--bind` and `--dep` flags | Docker labels or a bindings file | the `Pacto` resource and its Service |
| Privileges | none required; root or eBPF capabilities for full results | root or eBPF capabilities | a privileged DaemonSet |
| What it can prove broken | listeners in both modes; failed connections with eBPF only | listeners and failed connections | listeners and failed connections |
| Where the answer goes | your terminal and exit code; optionally an Evidence Server | your terminal or log; optionally an Evidence Server | the `Pacto` resource status |
| Platform | Linux; elsewhere the command runs unobserved | Linux, or a Docker Desktop container on macOS and Windows | any Linux node |
| Guide | [Run a command](runtime-run.md) | [Watch a host](runtime-agent.md) | [The Kubernetes way](integrations/kubernetes/runtime-conformance.md) |

Pick `pacto run` for a CI job or a single process you start yourself. Pick
`pacto agent` for long-running services on a Docker host or a server. Pick the
node agent when your services already run on Kubernetes with the Pacto
operator. On Kubernetes the node agent is `pacto agent` too, deployed by the
operator on every node.

## Requirements

Full observation needs:

- **Linux 5.11 or later**, or 5.8 to 5.10 with the `SYS_RESOURCE` capability
  (root has it), so the kernel lets Pacto load its program.
- **BTF**, the type information the kernel publishes at
  `/sys/kernel/btf/vmlinux`. Current distribution kernels ship it.
- **Root**, or the `BPF`, `PERFMON` and `SYS_PTRACE` capabilities.
- **cgroup v2** to tell apart workloads that share the host's network, such as
  systemd units, host-network containers and `pacto run` sessions. A *cgroup*
  is the kernel's label for a group of processes; every container and systemd
  unit gets one. Without cgroup v2, only workloads with their own network stack
  (most containers, every pod) can be told apart.

Without these, `pacto run` still works in an unprivileged mode. It reads the
command's open sockets from `/proc` once a second. It sees every listener, so a
missing one is still reported. It sees a connection only while it is open at a
read, and a failed connection never, so a dependency is reported reachable or
"don't know", never unreachable. `pacto agent` has no unprivileged mode.

## What stays unverified off Kubernetes

On Kubernetes the operator also reads the cluster API, which knows things the
network alone cannot show. On a plain host nobody reports them:

| Contract field | On Kubernetes | Elsewhere |
| --- | --- | --- |
| `interfaces[].visibility` | read from the Service, Ingress and load balancer | unverified, listed in `coverage.unverified`; the status does not change |
| `dependencies[].compatibility` | the provider's deployed contract version | unverified, listed in `coverage.unverified` |
| `state.persistence.scope` | read from the pod's volume claims | unverified, listed in `coverage.unverified` |
| `workload`, `state.persistence.durability: persistent`, required `configurations` | read from the workload, volumes and bound ConfigMaps | no source: the contract reads `Unknown` with `EVIDENCE_MISSING` |
| a call to another Pacto service no dependency declares | `DEPENDENCY_UNDECLARED` | not reported: the host does not know which peers are Pacto services |

The protocol an interface speaks (`interfaces[].type`) and the health and
metrics capabilities are checked everywhere, by probing the bound port.

The [field coverage](integrations/kubernetes/field-coverage.md) reference says,
for every contract field, what each of the three ways can verify, and names the
tests that prove it.

## See also

- [Evidence](evidence.md): how a remote host signs what it saw and reports it to
  your platform.
- [The model](model.md#collectors-and-the-evidence-boundary): how observations
  become findings.
