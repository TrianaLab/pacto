---
# Why the <h1 hidden> below: see the note beside the visible heading in
# overrides/home.html. Keep this note here, in the YAML front matter — an HTML
# comment in the body is served to every visitor.
template: home.html
---

<h1 hidden>Know which services a change breaks before it ships.</h1>

## What is Pacto? { #what-is-pacto }

Pacto tells you which services a change breaks before it ships, and checks
what is actually running. You describe each service once, in a contract: one
YAML file that says who owns the service, what it offers, which services it
calls and which of their versions it accepts. It lives in the OCI registry
you already run. `pacto diff` says whether a change is breaking and
`pacto impact` says which consumers it breaks once Pacto can see your fleet.
The [runtime checks](#check-what-is-actually-running) compare a running
service with its contract.

When you are ready, Pacto's opt-in delivery component (alpha, Kubernetes)
ships the changes the contract graph clears. Flux or Argo CD still apply every
version. [GitlessOps](manifesto.md#what-gitlessops-means) means signed
artifacts in your registry and evidence decide which version runs where, while
your code and manifests stay in git. Delivery starts by watching and climbs
[the autonomy ladder](autonomy.md), from observe (watch only) to autonomous,
set per [Stage](glossary.md#stage). A person confirms every step up. A change
that breaks a consumer waits for that consumer's owners, and when Pacto cannot
see enough it says [Unknown](glossary.md#unknown) and waits for a person.

A contract looks like this:

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

The `compatibility` range above records which versions of `auth` this
revision accepts. Each consumer's range lets Pacto answer per consumer when
auth is about to ship 3.0:

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

`pacto impact` finds consumers only in the sources you give it. Pass the
consumer contracts with `--root`, or read a live fleet with `--k8s` or
`--evidence-url`. With `--local` alone it cannot know who else consumes the
service, so "No affected consumers" is not evidence of safety.

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

Pacto checks what a running service does on the network against what its
contract says, on Linux, and reports only what it can confirm:

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
Docker host or a server and the Kubernetes operator can deploy the same agent
on every node (opt-in). See [Runtime conformance](runtime-conformance.md) for
the kernel and privilege requirements.

## What delivery needs

Contracts work anywhere, and the checks above work on Linux. Delivery, the
part that moves versions, is alpha: its settings and commands may still
change. It needs:

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

## Agents take the same path

An AI agent asks for a delivery change over MCP the way a person or a CI job
does: it creates an Operation, and the promoter plans and checks it. Agents
get no approve tool, and the admission policies the chart installs with
delivery refuse an approval from an agent. Kubernetes RBAC still governs
what the agent's identity may do outside Pacto.
[Agents take the same path](manifesto.md#agents-take-the-same-path) says what
the policies close and what your platform closes itself. The
[delivery security model](integrations/kubernetes/delivery-security.md) gives
the detail.

## Where to start

| You want to | Read |
|---|---|
| Know what Pacto is and whether it fits | [Who breaks when a dependency ships](#who-breaks-when-a-dependency-ships-a-new-version), the [live demo](examples/dashboard-demo.md) and [the autonomy ladder](autonomy.md) |
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
