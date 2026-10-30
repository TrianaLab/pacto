# Why Pacto

A team ships a change to its service, and a service another team owns breaks.
The pull request looked safe: a minor version bump and a few lines of YAML.
Review answers whether the change is correct. It rarely answers who the change
breaks, because that knowledge sits in other teams' code.

Pacto tells you which services a change breaks before it ships, and checks
what is actually running. This page follows one change through review twice,
first without Pacto and then with it.

## One change, two ways

The `commerce` team owns `orders`. It publishes version 2.4.0 with two
changes:

- The order `status` in the `orders-api` response gains a new value,
  `refunded`. The value lives in a shared component that the response
  references with `$ref`.
- The deprecated `orders-v1` interface now goes away on 2026-12-01 instead of
  2027-06-01.

```yaml title="orders-2.4.0/interfaces/orders-api.yaml (excerpt)"
components:
  schemas:
    OrderStatus:
      type: string
      enum: [pending, paid, shipped, refunded]   # 2.3.0 had pending, paid, shipped
```

The `finance` team owns `billing`. Its contract accepts any 2.x version of
`orders` and relies on per-key ordering of the `order-events` stream:

```yaml title="billing/pacto.yaml (excerpt)"
dependencies:
  - name: orders
    ref: ../orders
    required: true
    compatibility: "^2.0.0"
    requires:
      - interface: order-events
        ordering: per-key
```

### Without Pacto

The reviewer sees one added enum value in a components file, a date moved in
a deprecation notice and a minor version bump. Adding a value looks additive,
so the reviewer approves. Nothing in the diff names `billing`, and nobody on
the `commerce` team knows that `billing` decodes `status` into a closed type.

The release goes out. The first refunded order reaches `billing`, which
rejects the response, and invoices for that order stop. On 2026-12-01 the
`orders-v1` endpoint disappears, and `billing` learns about the new date from
its error rate.

### With Pacto: validate the new revision

`pacto validate` checks the contract in stages and names the next command to
run:

```console
$ pacto validate orders-2.4.0
orders-2.4.0 is valid
PASS load
SKIP lock         no pacto.lock
PASS structural
PASS cross-field
PASS policy
SKIP environments no environments declared
SKIP guarantees   no dependency declares requires
SKIP readiness    not requested (--readiness)
SKIP closure      not requested (--closure)
Next: pacto impact PUBLISHED_REF orders-2.4.0
```

### With Pacto: diff the two revisions

`pacto diff` compares 2.3.0 with 2.4.0. It resolves the `$ref` and judges the
enum where the response uses it. A new value in a response is `BREAKING`,
because a consumer that reads the old set of values cannot read the new one:

```console
$ pacto diff fleet/orders orders-2.4.0
Classification: BREAKING
Required bump: major
Changes (3):
  [NON_BREAKING] service.version (modified): service.version modified [2.3.0 -> 2.4.0]
  [BREAKING] openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.properties.status.enum[refunded] (added): openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema.properties.status.enum refunded added (in #/components/schemas/OrderStatus) [+ refunded]
  [POTENTIAL_BREAKING] interfaces.lifecycle.sunset (modified): interfaces.lifecycle.sunset modified: orders-v1 sunset moved earlier, from 2027-06-01 to 2026-12-01 [orders-v1: 2027-06-01 -> orders-v1: 2026-12-01]
breaking changes detected
```

The command exits 1. The `Required bump` line says the change needs a major
version, so 2.4.0 claims less than the change does. The reason names the
component, `#/components/schemas/OrderStatus`, so the reviewer knows which file
to read.

### With Pacto: list the consumers

`pacto impact` joins the diff with the contracts in the `fleet` folder and
names the services that depend on `orders`:

```console
$ pacto impact fleet/orders orders-2.4.0 --local fleet
Impact: orders 2.3.0 -> 2.4.0
Classification: BREAKING
Breaking changes: 1
Potentially breaking changes: 1
Affected consumers (1):
  billing                      direct     confidence=contractual  compat=compatible   owner=finance
    note: compatible only means the range ^2.0.0 accepts 2.4.0; this change is BREAKING, so check that billing does not use what it removes or changes
Release gate: unverified
unverified: the change is BREAKING and 1 consumer depends on orders, but none is shown to be both incompatible and running. Review each consumer, and add a source that knows where services run (for example --target-state) to tell deployed impact apart
```

The command exits 2. The reviewer now has a name and an owner: ask `finance`
whether `billing` handles `refunded`. The range `^2.0.0` accepts 2.4.0, and
the note says that a range match leaves the breaking change to check. A local
folder knows no deployments, so the gate is `unverified`. With a source that
knows where services run, such as `--target-state` or `--k8s`, the gate can
tell deployed consumers apart. See [impact analysis](impact.md).

### With Pacto: the consumer's next build

Once 2.4.0 is published, the `billing` pipeline checks the contracts it
depends on. The new sunset reaches `finance` as a warning with the date and
the successor:

```console
$ pacto validate --closure billing
billing is valid
…
PASS guarantees
SKIP readiness    not requested (--readiness)
PASS closure
  WARN  [DEPENDENCY_INTERFACE_DEPRECATED] dependencies[0]: orders@2.4.0 interface orders-v1 is deprecated since 2026-09-01, sunset 2026-12-01; successor orders-api
Next: pacto impact PUBLISHED_REF billing
```

The `guarantees` stage passes because `orders` still offers per-key ordering.
When a provider stops meeting a requirement, the stage fails. Here `billing`
asks for global ordering instead:

```console
$ pacto validate billing
billing is invalid
…
FAIL guarantees
  billing/pacto.yaml:16:5: ERROR [GUARANTEE_UNMET] dependencies[0].requires: billing requires order-events ordering global from orders, which offers per-key
…
Next: fix dependencies[0].requires at billing/pacto.yaml:16:5 [GUARANTEE_UNMET], then run pacto validate billing
validation failed with 1 error(s)
```

## What changes for each role

- **Provider team.** Run `pacto diff` before you tag a release. The
  `Required bump` line tells you which version the change needs, and
  `interfaces[].lifecycle` tells consumers when an interface goes away.
- **Consumer team.** Declare a `compatibility` range and the guarantees you
  rely on in `dependencies[].requires`. `pacto validate --closure` warns you
  about each deprecated interface and fails when a provider stops meeting a
  requirement.
- **Reviewer.** Read the `pacto impact` output on the pull request. It names
  the classification, each affected consumer and its owner.
- **Platform team.** Run `pacto impact` in CI with the sources that know your
  fleet, such as `--oci`, `--k8s` or `--target-state`. See
  [who breaks in CI](ci.md#on-every-pull-request-who-breaks).
- **On-call.** The operator and `pacto run` compare a running service with its
  contract and report findings such as `INTERFACE_ABSENT` and
  `DEPENDENCY_UNREACHABLE`. See [runtime conformance](runtime-conformance.md).
- **Agents.** The MCP tools `pacto_check` and `pacto_impact` return the same
  stages, `Next` line and consumer list a person sees. See
  [AI assistants (MCP)](mcp-integration.md).

## Start with a pilot

Pick two or three services you already run where one calls another. A pilot
needs no change to service code.

1. [Pactify each service](pactify.md): give it a contract that describes what
   it offers and which versions of its providers it accepts.
2. Add `pacto diff` and `pacto impact` to each pull request. See
   [CI](ci.md) and [GitHub Actions](github-actions.md).
3. Add `pacto validate --closure` to each consumer's build, so deprecations and
   unmet guarantees reach the consumer team.

Measure the pilot for a few release cycles:

- Breaking changes `pacto diff` reports before merge, and how many of them the
  author did not expect.
- Review time on pull requests that bump a provider version.
- Consumers that hear about a sunset from `pacto validate --closure` before the
  sunset date.

Expand when the pilot teams act on the impact output during review. Add the
services that call the pilot services next, because each new consumer makes
the impact answer more complete. The [tenets](manifesto.md#tenets) explain the
design choices behind each check.

## Limits and other tools

Pacto lists what it cannot do yet in the
[known limitations](https://github.com/TrianaLab/pacto#known-limitations).
[How Pacto relates to other tools](comparison.md) compares it with the tools
you may already use.
