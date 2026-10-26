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
or Argo CD, the tools that keep a Kubernetes cluster running what you ask for,
still apply the version.

GitlessOps, in one line: which version runs where is set from signed
artifacts in your container registry and from evidence, not from commits to
git. Your code and your
deployment files stay in git; only that one line moves out.
[What GitlessOps means](manifesto.md#what-gitlessops-means) gives the three
rules behind it.

Pacto starts by watching. It earns the right to ship on its own one step at a
time, and a person confirms every step: that is
[the autonomy ladder](autonomy.md). Its four steps, called rungs, are observe
(watch only), advise (a person asks for or approves every change), auto for non-breaking
and autonomous. The rung is set per Stage, meaning one service in one
environment, such as payments in prod. It asks only the teams a change affects,
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
accepts, and `pacto.lock` pins every dependency, direct or indirect, to the
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
itself rather than reaching it through another service, and `contractual` means
the answer comes from the declared ranges alone: Pacto has not also seen the
call happen at runtime.

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
programs the kernel runs on events). It never reads payloads or secrets, and
when it cannot see it says "don't know" instead of guessing. Run it three ways:
`pacto run` wraps one command (a laptop, a CI job), `pacto agent` watches a
Docker host or a server and the Kubernetes operator deploys the same agent on
every node. See [Runtime conformance](runtime-conformance.md).

## What delivery needs

Contracts and the checks above work anywhere. Delivery, the part that moves
versions, is alpha: its settings and commands may still change. It needs:

- Kubernetes 1.30 or later, with Flux (Argo CD works too, as an experimental
  option).
- Your container registry, to hold contracts and versions.
- For signed records, and so for the two automatic rungs, a registry that
  serves the OCI Referrers API: the feature that lets a registry list the
  files attached to an image, here the signed records. zot, an open source
  registry, does. GitHub's registry does not today, so with it a Stage can
  observe and advise but never ship on its own. For any other registry,
  check its documentation for "referrers API".
- For checks on error rates after a release, traces in
  [Mira](https://github.com/trianalab/mira), an OpenTelemetry trace store, or
  a check you call over HTTP. Without them Pacto still checks the contract.
- For a person to approve anything, a cluster that can tell how recently that
  person signed in to your company login. That works where you set the
  Kubernetes API server's flags yourself (tested on kind). It is not verified
  yet on EKS, GKE and AKS. Where it does not work, nobody can approve, so
  nothing can be raised up the ladder; see
  [where approving works](integrations/kubernetes/delivery-identity.md#where-approving-works).

Trying it costs nothing: a new service starts on observe and moves nothing,
and the [live demo](examples/dashboard-demo.md) runs in your browser.

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

Pacto delivery builds on two ideas from others:
[ConfigHub](https://confighub.com)'s configuration as data and Anthropic's
[Scion](https://www.anthropic.com/research/redesigning-source-control).
The contract graph is ours, and all of Pacto is open source under the MIT
licence.
