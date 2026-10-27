---
# Why the <h1 hidden> below: see the note beside the visible heading in
# overrides/home.html. Keep this note here, in the YAML front matter — an HTML
# comment in the body is served to every visitor.
template: home.html
---

<h1 hidden>Describe your services once. Pacto decides what ships.</h1>

## What is Pacto? { #what-is-pacto }

Pacto is continuous delivery for the
[GitlessOps](manifesto.md#what-gitlessops-means) era: signed artifacts and
evidence decide which version runs where, while your code and your deployment
files stay in git. You describe each service once, in a contract. Pacto works out what each change does to the services
that depend on it, and decides which version runs in each environment. Flux
or Argo CD, the tools that keep a Kubernetes cluster running what you ask for,
still apply the version.

Pacto starts by watching. It earns the right to ship on its own one step at a
time, and a person confirms every step: that is
[the autonomy ladder](autonomy.md), from observe (watch only) to autonomous,
set per [Stage](glossary.md#stage). It asks only the teams a change affects,
and when it cannot see enough it says [Unknown](glossary.md#unknown) and waits
for a person.

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
accepts, and an optional `pacto.lock` pins every dependency, direct or indirect, to the
exact published build. So when auth is about to ship 3.0, Pacto answers per consumer:

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

`pacto diff` lists the breaking change itself. `checkout` accepts `^2.0.0`
and 3.0.0 falls outside it. `reporting` accepts
`>=2.0.0 <4.0.0` and 3.0.0 is inside. `direct` means each one declares auth
itself. `contractual` means the answer comes from the declared ranges alone,
without a runtime observation of the call.

You check one file when you write it, compare it with the previous revision
and then compare it with what is running.

## What each command answers

| Command | Question it answers |
| --- | --- |
| `pacto validate` | Is this contract legal? |
| `pacto diff` | Is my change breaking for anyone who uses it? |
| `pacto impact` | Which consumers, and where do they run? |
| The [operator](integrations/kubernetes/overview.md), a controller you install in the cluster | Does reality still match what was declared? |

`validate` and `diff` pay off at one service; `impact` and the graph of who
calls whom pay off at the second consumer.

## Check what is actually running

Pacto can check what a running service actually does on the network against
what its contract says, on any Linux machine, and tells you only what it can
prove:

| What it catches | Finding |
| --- | --- |
| A process reports healthy but nothing listens on the declared port | `INTERFACE_ABSENT` |
| A declared dependency the service keeps failing to reach | `DEPENDENCY_UNREACHABLE` |
| A call to another Pacto service no dependency declares (seen on Kubernetes or by `pacto agent`) | `DEPENDENCY_UNDECLARED` |

```bash
pacto run --contract ./payments --bind api=8080 --fail-on noncompliant -- ./payments-server
```

Pacto watches TCP connections in the Linux kernel with eBPF (small, safe
programs the kernel runs on events). It reads connection metadata, never
payloads or secrets. When it cannot see something, it says so. Run it three ways:
`pacto run` wraps one command (a laptop, a CI job), `pacto agent` watches a
Docker host or a server and the Kubernetes operator deploys the same agent on
every node. See [Runtime conformance](runtime-conformance.md).

## What delivery needs

Contracts and the checks above work anywhere. Delivery, the part that moves
versions, is alpha: its settings and commands may still change. It needs:

- Kubernetes 1.30 or later, with Flux (Argo CD works too, as an experimental
  option).
- Your container registry, to hold contracts and versions.
- For signed records, and so for the two automatic levels, a registry that
  serves the OCI Referrers API: the feature that lets a registry list the
  files attached to an image, here the signed records. zot, an open source
  registry, serves it. GitHub's registry does not serve it today, so a Stage
  that uses it stays on observe or advise. For any other registry,
  check its documentation for "referrers API".
- For checks on error rates after a release, traces in
  [Mira](https://github.com/trianalab/mira), an OpenTelemetry trace store, or
  a check you call over HTTP. Without them Pacto still checks the contract.
- For a person to approve anything, a cluster that can tell how recently that
  person signed in to your company login. That works where you set the
  Kubernetes API server's flags yourself (tested on kind). EKS, GKE and AKS
  are not verified yet. Where it does not work, nobody can approve, so no Stage
  can move up the ladder. See
  [where approving works](integrations/kubernetes/delivery-identity.md#where-approving-works).

To try it, start a new service on observe, where Pacto moves nothing, or open
the [live demo](examples/dashboard-demo.md) in your browser.

## Where to start

| You want to | Read |
|---|---|
| Know what Pacto is and whether it fits | [The autonomy ladder](autonomy.md), [What GitlessOps means](manifesto.md#what-gitlessops-means) and the [live demo](examples/dashboard-demo.md) |
| Pactify your first service (give it a contract and connect it to delivery) | [Quickstart](quickstart.md), then [Pactify a service](pactify.md) |
| Approve or unblock a change | [Decide, unfreeze and roll back](pactify-decide.md) |
| Verify a record | [Check a record without Pacto](verify-a-record.md) |
| Drive Pacto from an AI agent | [AI assistants (MCP)](mcp-integration.md) |
| Write and check contracts | [For developers](developers.md) and the [contract reference](contract-reference/index.md) |

New words are explained in the [glossary](glossary.md).

Pacto delivery builds on Flux's Gitless GitOps, ConfigHub's configuration as
data and Anthropic's Scion; [the manifesto](manifesto.md#credits) says what it
takes from each. The contract graph is ours, and all of Pacto is open source
under the MIT licence.
