---
# Why the <h1 hidden> below: see the note beside the visible heading in
# overrides/home.html. Keep this note here, in the YAML front matter — an HTML
# comment in the body is served to every visitor.
template: home.html
---

<h1 hidden>Describe your services once. Pacto decides what ships.</h1>

## What is Pacto? { #what-is-pacto }

Pacto is continuous delivery for the GitlessOps era. You describe each service
once, in a contract. Pacto works out what each change does to the services
that depend on it, and decides which version runs in each environment. Flux
or Argo CD still apply the version, and your code stays in git.

Pacto starts by watching. It earns the right to ship on its own one step at a
time, and a person confirms every step: that is
[the autonomy ladder](autonomy.md). It asks only the teams a change affects,
and when it cannot see enough it says Unknown and waits for a person.

Contracts are the foundation. A contract is one YAML file that says who owns
a service, what it offers, which services it calls and which of their
versions it accepts. It lives in the container registry you already run, and
it is useful on its own, before you turn on delivery:

```yaml title="pacto.yaml"
pactoVersion: "2.0"

service:
  name: checkout
  version: 4.2.0
  owner: { team: commerce, dri: alice }

interfaces:
  - name: rest-api
    type: openapi
    ref: interfaces/openapi.yaml       # the OpenAPI document you already maintain
    visibility: public

dependencies:
  - name: auth
    ref: oci://ghcr.io/acme/auth-pacto
    required: true
    compatibility: "^2.0.0"            # the range this revision accepts
```

## Who breaks when a dependency ships a new version

The `compatibility` range above records which versions of `auth` this revision
accepts, and `pacto.lock` pins every dependency below it to the exact published
build. So when auth is about to ship 3.0, Pacto answers per consumer:

```console
$ pacto impact ./auth-2.1.0 ./auth-3.0.0 --local .
Impact: auth 2.1.0 -> 3.0.0
Classification: BREAKING
Breaking changes: 1
Potentially breaking changes: 0
Affected consumers (2):
  checkout                     direct     confidence=contractual  compat=incompatible owner=commerce
  reporting                    direct     confidence=contractual  compat=compatible   owner=analytics
```

`checkout` accepts `^2.0.0` and 3.0.0 falls outside it. `reporting` accepts
`>=2.0.0 <4.0.0` and 3.0.0 is inside. `direct` means each one declares auth
itself rather than reaching it through another service, and `contractual` means
the dependency is declared with a usable range and nothing observed it running.

One file, checked when you write it, compared against the previous revision,
then compared against what is actually running.

When Pacto cannot observe something it says `Unknown`. It never reports it as a
pass.

## What each command answers

| Command | Question it answers |
| --- | --- |
| `pacto validate` | Is this contract legal? |
| `pacto diff` | Did I break my own consumers? |
| `pacto impact` | Which consumers, and where do they run? |
| The [operator](integrations/kubernetes/overview.md) | Does reality still match what was declared? |

`validate` and `diff` pay off at one service; `impact` and the graph pay off at
the second consumer.

## Check what is actually running

Pacto can check what a running service actually does on the network against
what its contract says, on any Linux machine, and tells you only what it can
prove:

| What it catches | Finding |
| --- | --- |
| A process reports healthy but nothing listens on the declared port | `INTERFACE_ABSENT` |
| A declared dependency the service keeps failing to reach | `DEPENDENCY_UNREACHABLE` |
| A call to another Pacto service no dependency declares (Kubernetes, `pacto agent`) | `DEPENDENCY_UNDECLARED` |

```bash
pacto run --contract ./payments --bind api=8080 --fail-on noncompliant -- ./payments-server
```

Pacto watches TCP connections in the Linux kernel with eBPF (small, safe
programs the kernel runs on events). It never reads payloads or secrets, and
when it cannot see it says "don't know" instead of guessing. Run it three ways:
`pacto run` wraps one command (a laptop, a CI job), `pacto agent` watches a
Docker host or a server and the Kubernetes operator deploys the same agent on
every node. See [Runtime conformance](runtime-conformance.md).

## Where to start

| You want to | Read |
|---|---|
| Know what Pacto is and whether it fits | [Delivery](integrations/kubernetes/delivery.md), [the autonomy ladder](autonomy.md), [What GitlessOps means](manifesto.md#what-gitlessops-means) and the [live demo](examples/dashboard-demo.md) |
| Pactify your first service | [Quickstart](quickstart.md), then [Pactify a service](pactify.md) |
| Approve or unblock a change | [Decide, unfreeze and roll back](pactify-decide.md) |
| Verify a record | [Signed records and history](integrations/kubernetes/delivery-records.md) |
| Drive Pacto from an AI agent | [AI assistants (MCP)](mcp-integration.md) |
| Write and check contracts | [For developers](developers.md) and the [contract reference](contract-reference/index.md) |

New words are explained in the [glossary](glossary.md).

Pacto delivery builds on two ideas from others:
[ConfigHub](https://confighub.com)'s configuration as data and Anthropic's
[Scion](https://www.anthropic.com/engineering/redesigning-source-control).
The contract graph is ours, and all of Pacto is open source under the MIT
licence.
