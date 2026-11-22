# The service page

Open a service in the dashboard to see everything Pacto knows about it on one
page: who owns it, which contract it runs, what it depends on and where it runs.
An empty area always says why it is empty.

## What the page answers

The facts at the top name the service's domain, its owner, the contract it runs,
its capabilities and the sources that saw it. The operational summary shows
compliance, how certainly each target is matched to a revision, how recently it
was observed and how the declared dependencies compare with observed traffic.
The sections below list findings, observed traffic, resources, operational targets,
environments, delivery, revisions, expected dependencies, dependents, signatures, policies,
configuration, recent evidence and the limitations that make any of it
uncertain.

## Owner and contract

The Owner fact reads **No declared owner** when a readable contract revision
exists and none of them names an owner. It reads **Owner unknown** when Pacto
could not read any of the service's contracts and no operational target reports
the contract it runs, so Pacto cannot tell whether an owner is declared. The
fleet counts both cases as unowned, and both link to this service's `unowned`
attention item. For an unknown owner that item asks you to make one of the
service's contracts readable. A declared owner links to the revision that declares it, under
**Declared in**, where the owner's contacts are listed. The Contract fact links
the newest revision in use, or the newest known revision when nothing runs the
service.

A mark after an owner no revision Pacto read declares, including a resource's
provider owner, says in its tooltip where it comes from.

- **Where the values come from**, a fold closed by default, lists each field
  of the current revision with one mark whose tooltip says declared, accepted
  (and by whom), attested or inferred, with its source and when it was read.
  It opens with [contract completeness](operational-graph-tallies.md#contract-completeness):
  the percentage, then one row per section that applies, with a mark whose
  tooltip says declared, inferred only, missing or unknown and why. A gap
  Pacto can fill from what it inferred adds **YAML to declare it** with a copy
  button. The fold shows for every service but a golden path or an inferred
  service, and its field rows for one whose current revision carries a declaration or that
  replaced an inferred service through one. Inferred values are muted, and
  clearing **Show inferred values** hides them; the fold is then their one list
  on the page, in place of **Inferred by Pacto**. Below them, the inferred services the service
  [replaced through a declaration](operational-graph-inferred.md#when-a-declaration-replaces-an-inferred-service)
  and what the newest one's workload showed, or a count of those whose
  workload you cannot see.

## Dependencies

**Expected dependencies** lists every dependency the contract declares. A
dependency on a fleet service links to that service. An **external** dependency
is delivered outside Pacto and shows the version the contract declares. An
**unresolved** dependency matches nothing Pacto knows. Dependencies hidden by
your Kubernetes permissions are counted and never named. The section count is
the **Declared dependencies** count in the dependency drift row, which also
counts the dependencies declared and observed, declared and never observed,
observed and never declared, and unresolved. **Outside accepted range** counts
dependencies whose provider version falls outside the range the contract
accepts. **Cannot be observed** counts dependencies Pacto has no observation
data for, so it cannot tell whether the service talks to them.

## Resources

Each card is one resource the service consumes.

![The Resources section of catalog-service's service page](assets/screenshots/dashboard-service-resources-light.png#only-light)
![The Resources section of catalog-service's service page](assets/screenshots/dashboard-service-resources-dark.png#only-dark)

| Part | Shows |
|---|---|
| Heading | The provider, linked, its offering or **no offering**, the kind of use, the accepted range and the provider's lifecycle. A use of a provider with no offering appears once an adapter reports on it |
| Provider owner | The provider's owner and whether the provider answers where it runs, apart from this resource's phase |
| Requested (declared) | On the configuration row only: the provider revision, the configuration entry and the values the contract asks for |
| Provisioning (observed) | The phase per environment with the adapter, **signed** or **unsigned** and the external reference. Select a phase to see the request as a drawing. A phase is never the service's health; see [Resources a service consumes](provision-infrastructure.md#resources-a-service-consumes) |
| Usage | **Not reported** with the reason, and a link to the service in Mira when Mira is configured |
| Below the cards | **At least N resources from providers you may not see**, counted from this service's own contract and reports, then the sentence: Resize or delete runs in the provider's tools. pacto_edit refuses a request the provider's schema rejects, and pacto validate and push fail on it |

## Targets and delivery

Each operational target shows its environment, or **Environment unknown** when
no source reported one. The **Delivery** section lists the service's Stages:
the version each environment runs and whether it is verified, failed, frozen or
waiting. A Stage belongs to the service when its `spec.service.name` equals the
service name exactly, so `orders` never shows the Stages of `orders-api`. A
Stage that names a registry domain in `spec.service.domain` shows only on the
page of the service in that domain. A Stage that names no domain shows on the
page of every service with that name. When the service has a domain, such a row
reads **Stage names no domain**: the match is by service name only. When
no Stage matches, the section says so: delivery is opt-in per service. When the
dashboard does not serve delivery, the section says why, and when Pacto cannot
reach the cluster it shows the error with a retry. When Pacto could not list
every namespace, the section reads **No Stages of this service that you can
see** and names what it could not list.

## Stages, actions and why

Each Stage row shows its state, the version it runs linked to its revision, the
contract behind it and the source revision `pacto publish` stamped (an
annotation, not signed). A Stage that cannot promote says why and how to fix
it. The row shows the newest re-check, marked stale when it is about an
earlier version, and the newest change. A change read from an Operation's
status is marked **from status, not signed**.

The buttons under a row are the actions you may take: promote, unfreeze,
freeze and undo. An action you may not take stays disabled, with the reason
beside it. Every action proposes with your identity.
**Why** asks the cluster, as you, what holds the Stage back, with each fix as
a command when you may run it.

## Environments

The **Environments** section lists each environment the current revision
declares or a target you can see reports, alphabetically. It never shows a
promotion order: the Delivery section does. Each fact says whether it is
declared, observed or inferred.

![The Environments section of storefront-web's service page](assets/screenshots/dashboard-service-environments-light.png#only-light)
![The Environments section of storefront-web's service page](assets/screenshots/dashboard-service-environments-dark.png#only-dark)

| Cell | Shows |
|---|---|
| Declared | Whether the environment's values pass, its lifetime (**Lifetime not declared** when absent, never persistent), its expiry, or **Expired (inferred, as of ...)** once `expires` has passed, and its owner: declared, **service owner, inferred** or **Owner unknown**. An environment only targets report reads **Not declared by** the current revision |
| Observed | The targets you can see labelled with the environment, linked to that target list, with **(incomplete)** and the reason when you may not see every target, and the revisions they run |
| Provisioning | The phases of the resources in the Resources section there (including those whose provider declares no offering), **Unknown until a provider reports** or why provisioning is not tracked there. A provider you may not see makes the cell Unknown |
| Stage | On a dashboard that serves delivery, the Stages whose `spec.environment` equals the name exactly, with their state. **Stages unknown: partial listing** when Pacto could not list every Stage. A Stage environment that matches no row is listed as **Stage only** |

Below the rows:

- Targets with no environment label, or whose sources disagree, form one
  **Unknown** row.
- **Differences between environments** lists each value the declared
  environments disagree on, one row per environment, with **not set** for a
  value an environment leaves out.
- **Environment changes** opens the **What changed in this service?**
  section filtered to environment changes; see
  [a service's changes](service-changes.md).
- An expired environment is never deleted; see
  [temporary environments](contract-reference/environments.md#temporary-environments).

## Delivery links

The **Delivery history** and **Open in Delivery** links open the delivery
history and the environments board narrowed to the same Stages. Both pages name
the service they show and link back to every service. The dashboard API filters
Stages the same way with the `service` and `domain` parameters of
`/api/delivery/environments` and `/api/delivery/history`.

## Golden paths and created services

The page of a [golden path](golden-paths.md) adds a **Golden path** section:

- **Create a service from it** holds a copyable CLI command and MCP call that
  name the path's newest revision by digest when a registry served it, or its
  folder when it was read from local files, or says why no command is
  available;
- **Signature** shows the path's signature check: who signed it and whether
  a publisher rule, pin or policy accepts that signer;
- **Plugins** lists the generate plugins the path names;
- **Requirements** lists the offerings and references the path asks for, each
  reference that does not resolve and how many are on services you may not
  see;
- **Services created from it** lists the services whose declared origin
  matches the path by content digest, and counts those you may not see.

A service created from a golden path shows a **Created from** fact labelled
declared: its contract records the origin, and Pacto did not observe the
creation. It links to the golden path and its revision, or shows the recorded
`ref` with the reason it matches no golden path. The **Creation status**
section lists the [creation checklist](golden-paths-reference.md#creation-checklist),
derived from what Pacto observes now.

## Readiness, policies and configuration

The readiness bar tallies every known revision as passing, below its own
threshold, assessment expired or not assessed, and each bucket opens those revisions. **Policies** and
**Configuration** show what the current revision declares. For a schema inside
the bundle, the page shows its title, description and properties, read from the
bundle when the page is requested. A policy that references another contract
links to that contract, which holds its content. A configuration that
references another contract links to it and, once the reference resolves,
shows the provider, its version, the selected configuration and that
schema's content inline. An unresolved reference shows why. A schema Pacto
cannot read shows why too.

## Bundle signatures and referrers

Only a registry source checks signatures. When the dashboard reads a revision
from a registry, it lists the OCI referrers attached to the revision's digest,
reads who signed each Sigstore signature and checks that signer against a
publisher rule, a person's pin or a signed delivery policy
([which publishers Pacto accepts](integrations/kubernetes/delivery-security.md#which-publishers-pacto-accepts)).
`publish.sign` never decides. Revisions read from a local directory, the
cache or a catalog read **Not checked**.

| Status | When |
|---|---|
| `verified` | A signature verifies and its signer is accepted by the installation's rule, a pin, a verified policy or, for an [external system](external-catalog-reference.md), its release signer |
| `unverified` | A signature verifies, and no rule, pin or policy accepts its signer. The page names who signed in the reason and the rule's identity as the **Expected signer** |
| `unsigned` | No Sigstore bundle is attached to the digest, or the attached bundles hold no signature |
| `unknown` | Pacto could not decide: the registry could not list the referrers or returned only their first page, the revision has no digest, the trusted root could not be fetched, the checks ran out of time or the rule was `inferred`. The reason is shown |

- `source` says what decided: `rule`, `pin`, `policy`, `release`,
  `inferred` or `none`, shown as a mark on the revision page's **Checked
  against** row.
- When the dashboard reads the installation's `publishers.json` and pins from
  the cluster, it judges as the promoter does. Without them it infers a rule
  from where the revision lives, such as `ghcr.io/acme`, and an accepted
  signer reads `unknown` with source `inferred`: that rule matches any
  organization's CI on its own artifacts, so it never makes a signature
  verified.

A key or KMS signer reads `unknown` because the bundle carries no public key and
the dashboard never calls a contract's KMS. Legacy cosign `.sig` tags are not
read. On GHCR it reads `pacto publish`'s `pacto-sig-` tags, never the
`sha256-<hex>` index.

Each result is cached for 5 minutes, and a failed check for 1 minute. The
checks of one refresh share a 20 second budget. `pacto fleet` runs the same check on
each registry reference given with `--oci`. The result is the `provenance` field of each revision in
`pacto_fleet_get` and in `/api/fleet/entities/revision?key=<key>`.

## Declare an inferred service

An [inferred service](operational-graph-inferred.md) offers **Declare this
service**, a fold closed by default.

![The Declare this service panel of legacy-billing](assets/screenshots/dashboard-declare-panel-light.png#only-light)
![The Declare this service panel of legacy-billing](assets/screenshots/dashboard-declare-panel-dark.png#only-dark)

- **What it shows.** Opening it plans what
  [`pacto declare`](publishing.md#declare-a-running-service) would publish,
  reading the cluster with your own credentials: the version, the signer, one
  row per proposed range with an accept box and a mark for where it comes
  from, then `pacto.yaml`, `pacto.inferred.yaml` and the drafts, collapsed.
  Only a checked range is declared as accepted.
- **Who may.** A signed-in person, or anyone on a local dashboard. Signing,
  saving a draft and accepting one need `update` on the workload in its own
  cluster, the rule delivery applies to approvals. A sign-in link or a
  dashboard without sign-in plans nothing. The panel shows only on a
  dashboard with a registry to publish to (`--registry`, the chart's
  `registry` value). Elsewhere the page names the `pacto declare` command
  to run instead.
- **Sign.** With a signing client Fulcio trusts
  ([signing in the dashboard](integrations/kubernetes/dashboard-authentication.md#sign-declarations)),
  a person who signed in through OIDC gets **Sign as** their name. It asks for a
  fresh login with a second client, so the token Fulcio sees is never the
  session's, then shows the whole plan again and publishes on **Publish**.
- **Save draft.** Everywhere else, after a token sign-in or on a local
  dashboard, **Save draft** writes an unsigned, write-once `draft-<n>` tag on
  your behalf. A shared dashboard saves drafts only with its own push
  credential; without one the panel shows the reason and the command.
- **Accept a draft.** **Accept** on a draft written by `pacto declare --draft`,
  an agent through `pacto_declare` or this panel first shows that draft's own
  plan, with every change nothing backs, and then signs it. A draft that
  changed since you read it is refused as stale. Drafts never become versions.
- **The command.** The panel always shows the CLI command that does the same
  in a terminal, such as
  `pacto declare <workload> --registry <registry> --from-draft draft-<n>`.
  A second line gives the same command with `--pr`, which opens the pull
  request into the service's repository with your own Git token from your
  machine ([Open a pull request](publishing.md#open-a-pull-request)); the
  dashboard never holds a Git credential.
- **Links.** Attention items and observed callers with no contract link here
  with `?declare=1`, which opens the panel.
