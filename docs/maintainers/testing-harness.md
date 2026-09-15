---
# Contributor-internal. It is thorough, so it keeps outranking the reader
# pages on general queries; halve it rather than thin the page.
search:
  boost: 0.5
---

# Harness code and ownership

The code the acceptance scenarios share, and the rule that keeps a harness
from destroying something it did not create.

## Shared harness code

Every stable shared concern has **one** implementation.

`tests/acceptance/kind/lib.sh` is that implementation for the cluster scenarios.
It owns process execution and pass/fail reporting, eventually-conditions and
timeouts, cluster lifecycle and teardown, image loading, chart packaging and
Helm invocation, port-forwarding, readiness waits, the in-cluster registry and
trust keypair, bundle publishing, and failure diagnostics. Every `*.sh` under
`tests/acceptance/kind/` sources it.

What stays in the scenarios is **scenario-specific orchestration**: their EXIT
traps, their fixtures, their own assertions, and anything a single scenario needs
in a form no other scenario shares.

Two behaviours in `lib.sh` are load-bearing and documented at their definition —
change them only with a reason:

- `pf` waits for the forward to be *ready*, not merely started, and reports on
  stderr. A port-forward that has not bound yet fails the next command with a
  connection error that reads like a product bug.
- `fail` writes to stderr for the same reason: call sites routinely silence a
  helper's chatty stdout, and a reason written to stdout would be silenced with
  it, leaving a run that exits 1 saying nothing.

`tests/acceptance/local/` has no cluster to manage and keeps its own small
helpers; `integrations/kubernetes/test/utils` serves the operator module.

## Loading an image into the kind node

`load_images` is the only way an image reaches a node, and it is deliberately
**not** `kind load docker-image`. That command pipes `docker save` into `ctr
images import --all-platforms` inside the node, which cannot work when the host
holds only part of a multi-platform image — the state Docker Desktop's
containerd image store leaves a pulled tag such as `registry:2` in:

```text
ERROR: failed to load image: command "docker exec ... ctr --namespace=k8s.io
images import --all-platforms --digests --snapshotter=overlayfs -" failed
Command Output: ctr: content digest sha256:46faa9a1...: not found
```

That digest is another platform's **manifest**, not the image. Four identities
are in play and none is interchangeable: the Docker image ID, the multi-platform
**index** digest, a per-platform **manifest** digest, and the **config** digest.
Under the containerd image store `docker image inspect --format {{.Id}}` reports
the index digest while the node reports the config digest, so kind's "already
present?" short-circuit can never match — it re-imports, and fails, every time.
Flattening the image by hand does not survive either: the scenario pulls the tag
again on the next run.

### What `kindload` does instead

`tests/acceptance/kind/kindload` (Go, unit-tested) owns the decisions:

1. the **node** is asked which platform it runs (`uname -m` in the node
   container) — not the host, not `runtime.GOARCH`, not an OS name;
2. the export is narrowed with `docker save --platform` when the Docker CLI in
   front of it accepts that flag, read off `docker save --help`: a capability
   check, not a product check;
3. the archive is proven **self-contained** before the node sees it — every
   descriptor it references, recursively, must have its content inside. This is
   exactly what `--all-platforms` demands, checked where the diagnostic can name
   the missing platform instead of inside the node where it is a bare digest;
4. `kind load image-archive` imports it, which has no identity short-circuit to
   get wrong;
5. `crictl` is asked, **on every node**, whether the reference now resolves to
   the config digest that was exported.

Step 5 is what lets a scenario write `imagePullPolicy: Never` and mean it: an
image the node pulled from Docker Hub under the same name carries a different
config digest and fails the check. Nothing on this path branches on an operating
system, so CI's classic image store and a Docker Desktop workstation execute the
same code.

### When it fails

`archive is not self-contained` means the export still carried a
platform whose content is not local, and the message names both the digest and
the platform — on a Docker CLI older than 28 there is no `docker save
--platform` to narrow with, so upgrade it. `resolves to ... not the loaded ...`
means the node already holds a different image under that name; remove it with
`ctr -n k8s.io images rm` in the node and re-run.

## The harness owns only what it created

The harness runs privileged, in the host's namespace, on machines it does not
know — so every fixed name it once used was a name it could have taken from
something else, and `ip link del` or `docker rm -f` before claiming one is
destroying a stranger's resource to make room. So each invocation derives a
random `RUN_ID` and suffixes its interfaces, its endpoint containers, its
registry and its image with it (inside `IFNAMSIZ`'s 15 characters, which
`ip link add` enforces rather than truncates). If a name is somehow already
taken, or every candidate `/30` is routed, the harness refuses before it mutates
anything. Cleanup is the same rule read backwards: a resource is recorded only
once *this* invocation has successfully created it, and only recorded resources
are removed.

Three things that rule has to survive:

- **A recorded name is not a recorded resource.** A container name is a lease
  that ends with the container holding it, so cleanup records the immutable
  container id instead — otherwise the endpoint the run shuts down early frees a
  name, and whoever takes it next is a stranger cleanup would delete. Cleanup
  holds no fixed name and sweeps no prefix.
- **Created and started are two events.** The daemon really does create a
  container and then refuse to start it — a host port somebody else published is
  enough — so the harness splits `docker create` from `docker start` and records
  the id between them. A container that never ran is still this invocation's to
  take away.
- **Ownership of an interface begins at `ip link add`, not at the preflight.**
  The preflight is a diagnostic: a name that reads as free can be taken before
  the next line runs, and `ip link add` is the atomic operation that says who
  won. A failure before it deletes nothing; a failure after it removes that pair
  and nothing else.

The two documented Compose project names are ownership too. `docker compose -p
NAME down -v --remove-orphans` needs no file — it is purely label-driven — so the
authority to run it comes from stage 0 having found both names holding nothing,
and the helper modes below, which exit long before stage 0, have no authority to
tear down a demo somebody is running.

## "Holding nothing" means every class that teardown removes

`down -v --remove-orphans` removes three — containers, named volumes and
networks. The network is the one that can be there alone: `up` creates it before
anything else and a `down` without `-v` leaves it standing, so a project that is
nothing but a network reads as empty to anything that looks only for containers
and volumes. Stage 0 therefore reads all three under each name and refuses on
any of them, and it arms the authority in a single assignment after both names
are through, so a refusal on the second never leaves the first one claimed.

### The selftest

`bash tests/acceptance/local/compose-demo.sh selftest` is the proof, and
`make test-acceptance-compose-selftest` runs it ahead of the acceptance in CI. It
plants sentinels — an interface, a harness-shaped container, an occupied `/30`,
an unrelated project under a name the documentation never uses and, under each
documented project name, a container, a network and a volume wearing the labels
`down -v` reads — then drives the harness into every path that could take one. It
asserts that a name already held is refused rather than deleted; that an
interface appearing *after* the preflight survives the create that loses the race
to it; that a failure later in the wiring removes only the pair this run made;
that no netfilter rule is touched on any of those ways out; that a claimed `/30`
is stepped over rather than hijacked; that a normal run, an induced failure and a
container the daemon refuses to start all end with the real `EXIT` trap taking
that run's resources and nothing else; that a documented name holding nothing but
a Compose network is refused by a real invocation — under either name, including
when the *second* is the occupied one — which keeps that exact network and arms
nothing; and that the planted projects, and the unrelated one beside them, still
hold every container, network and volume they did.
