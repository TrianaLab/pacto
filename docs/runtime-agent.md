# Watch a host with pacto agent

`pacto agent` is a small daemon that watches every workload on one Linux host
and checks each one against its contract, round after round. Use it for
long-running services on a Docker host, a VM or a bare-metal server.
[Runtime conformance](runtime-conformance.md) explains what it watches and what
the answers mean; this page shows how to run it.

On Kubernetes the operator deploys the same agent on every node. See [the Kubernetes way](integrations/kubernetes/runtime-conformance.md).

## Quick start on a Docker host

Label the container you want checked with its contract and its ports:

```bash
docker run -d --name payments \
  --label dev.pacto.contract=oci://ghcr.io/acme/payments:1.4.0 \
  --label dev.pacto.bind.api=8080 \
  --label dev.pacto.dep.postgres=db.internal:5432 \
  ghcr.io/acme/payments:1.4.0
```

Then start the agent in a container of its own, from the Pacto runtime image.
Set `PACTO_VERSION` to the Pacto release you run (`pacto version` prints it):

```bash
docker run -d --name pacto-agent --restart unless-stopped \
  --privileged --pid=host --cgroupns=host --network host --user 0 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /sys/fs/cgroup:/host/sys/fs/cgroup:ro \
  "ghcr.io/trianalab/pacto/dashboard:${PACTO_VERSION}" \
  agent --sink local --docker --cgroup-root /host/sys/fs/cgroup
```

`docker logs -f pacto-agent` shows one round every 30 seconds:

```text
2026-10-18T09:12:44Z
container/payments  Compliant  2/2 evaluated  oci://ghcr.io/acme/payments:1.4.0
```

Why each flag is there:

| Flag | Why |
| --- | --- |
| `--privileged`, `--user 0` | loading the eBPF program needs root or the `BPF`, `PERFMON` and `SYS_PTRACE` capabilities |
| `--pid=host` | the agent reads every process's sockets and cgroup from the host's `/proc` |
| `--cgroupns=host` and the `/sys/fs/cgroup` mount | the agent names each socket's workload from the host's cgroup tree |
| `--network host` | health and metrics probes reach host-network services at `127.0.0.1`; without it those probes are reported unsupported, with a note saying why |
| the Docker socket | the agent reads container labels; it only ever sends `GET` requests |

On macOS and Windows, run the same command against Docker Desktop: the agent
watches the containers inside Docker's Linux VM.

## Docker labels

| Label | Meaning |
| --- | --- |
| `dev.pacto.contract` | the contract: an `oci://` reference, or a bundle directory as the agent's container sees it |
| `dev.pacto.bind.<interface>` | the port the container serves the contract's interface on |
| `dev.pacto.dep.<dependency>` | the `host:port` the container reaches the contract's dependency at |

Only running containers with `dev.pacto.contract` are checked. On a daemon several projects share, add
`--docker-label-filter` to narrow that further: each one is a `key` or `key=value` label a container must
also carry, all of them must match and the flag repeats. `--docker-label-filter
com.docker.compose.project=demo` keeps the agent to one Compose project. Probes dial the
container's address on its first network, or `127.0.0.1` for a host-network
container. A directory contract is read from the agent's own filesystem, so
mount it into the agent container at the same path the label names.

## Bindings file

For workloads that are not containers, such as systemd services, processes or
`pacto run` sessions, describe them in a YAML file and pass it with
`--bindings`:

```yaml
# /etc/pacto/bindings.yaml
- match:
    unit: payments.service
  contract: /etc/pacto/contracts/payments
  interfaces:
    api: 8080
  dependencies:
    postgres: db.internal:5432
- match:
    container: 3f2a9c1b7d4e
  contract: oci://ghcr.io/acme/search@sha256:9b2d...
  host: 172.17.0.4
  interfaces:
    http: 9200
```

| Field | Meaning |
| --- | --- |
| `match` | exactly one of `unit` (a systemd unit), `container` (an id, at least 12 characters), `cgroup` (a cgroup v2 path), `session` (a `pacto run` session) or `pid` (a process; its workload is looked up at every round) |
| `contract` | an `oci://` reference or a bundle directory; a relative directory is relative to the file |
| `host` | the address probes dial; default `127.0.0.1` |
| `interfaces` | contract interface name to port |
| `dependencies` | contract dependency name to `host:port`; host names are resolved at every round |

Unknown fields are errors, so a typo fails at startup. `--bindings` and
`--docker` can be used together. A bindings-file workload counts as started
when its oldest running process started, so a connection from before that is
ignored.

## Undeclared dependencies

When a workload connects to another checked workload (a labelled container's
address on a bound port, or a bindings entry's `host` and port) that its
contract does not declare, the agent reports `DEPENDENCY_UNDECLARED`, named by
the other workload's contract service. A declared dependency is matched to a
checked workload by service name, contract reference or bound address. If one
matches none, it might be the very workload reached under another name, so the
agent reports no undeclared dependency for that workload at all.

## Where results go

`--sink` picks what happens to each round:

- **`local`** prints every workload's status and findings in `--output-format`
  (`text`, `json` or `markdown`). Nothing leaves the host.
- **`evidence`** signs each workload's evidence and sends it to an
  [Evidence Server](evidence.md), which stores it in your registry next to the
  contract revision. Your platform sees every host's results in one place.
- **`stdout`** prints the raw observations as JSON, one line per round, for
  debugging or for your own tooling.
- **`kubernetes`** is what the operator's node agent uses. The operator sets it.

### Sending evidence

Create a signing key on the host and give the public half to whoever runs the
Evidence Server:

```bash
pacto evidence keygen --producer host-eu-1 --key-id host-eu-1-2026
```

The Evidence Server keeps evidence only about exact contract revisions, so every
contract must be pinned by digest: `oci://registry.example.com/acme/payments@sha256:<digest>`.
The agent refuses to start when a bindings-file contract is not pinned, and
reports an error for a labelled container whose contract is not.

```bash
pacto agent --sink evidence --bindings /etc/pacto/bindings.yaml \
  --evidence-url https://evidence.example.com \
  --evidence-key /etc/pacto/host-eu-1-2026.key --evidence-key-id host-eu-1-2026 \
  --evidence-producer host-eu-1
```

[Evidence](evidence.md) covers the trust store and the server's `--subject`
list.

## Run it as a systemd service

On a server without Docker, install the `pacto` binary and run the agent as a
unit:

```ini
# /etc/systemd/system/pacto-agent.service
[Unit]
Description=Pacto runtime conformance agent
After=network-online.target

[Service]
ExecStart=/usr/local/bin/pacto agent --sink local --bindings /etc/pacto/bindings.yaml
Restart=on-failure
# Root loads the eBPF program; the agent opens no port and writes no files.
User=root

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pacto-agent
journalctl -u pacto-agent -f
```

## Tuning

| Flag | Default | Meaning |
| --- | --- | --- |
| `--publish-interval` | `30s` | how often a round is checked and printed |
| `--stabilization-window` | `2m` | how long a negative must last before it is reported |
| `--resync-interval` | `60s` | how often listeners are re-read from `/proc`, which heals a missed event |
| `--enable-probing` | off | also check each listening interface speaks the protocol its type implies |
| `--node-name` | `$NODE_NAME` | the host name in raw snapshots |

The agent needs Linux with BTF and, to tell workloads that share the host's
network apart, cgroup v2. [Requirements](runtime-conformance.md#requirements)
has the details, and the [CLI reference](cli-reference.md#pacto-agent) lists
every flag.
