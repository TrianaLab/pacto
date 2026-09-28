---
# Why the <h1 hidden> below: see the note beside the visible heading in
# overrides/home.html. Keep this note here, in the YAML front matter — an HTML
# comment in the body is served to every visitor.
template: home.html
---

<h1 hidden>One versioned contract per service</h1>

## What is Pacto? { #what-is-pacto }

A service's operational facts are scattered. Ownership is in a wiki. The API is
an OpenAPI file in another repository. What it depends on is implied by Helm
values and a Service name. What breaks if you change it is in someone's memory.

Pacto is an operational contract system. It puts those facts in one YAML file,
published to the container registry you already run.

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

## What a service catalog does not do

A catalog entry saying `dependsOn: auth` records that the edge exists. A Pacto
dependency records that this revision accepts `auth ^2.0.0`, and `pacto.lock`
pins the rest of the transitive closure by digest. So when auth is about to ship
3.0, Pacto answers per consumer:

```console
$ pacto impact ./auth-2.1.0 ./auth-3.0.0 --local ./services
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
| The operator | Does reality still match what was declared? |

`validate` and `diff` pay off at one service; `impact` and the graph pay off at
the second consumer.

## Where to start

- [What the live demo shows](examples/dashboard-demo.md) — the dashboard runs
  in your browser against a fixture fleet, nothing to install
- [Quickstart](quickstart.md) — an empty directory to a published bundle, about
  five minutes
- [Put it in your repository](github-actions.md) — validate and diff on every
  pull request
- [Contract reference](contract-reference/index.md) — every field and every
  validation rule
