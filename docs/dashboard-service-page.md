# The service page

Open a service in the dashboard to see everything Pacto knows about it on one
page: who owns it, which contract it runs, what it depends on and where it runs.
It also shows how the service is delivered, whether its revisions are ready and
signed and what its policies and configuration declare. Every fact the page cannot establish is
stated as missing, so an empty area always says why it is empty.

## What the page answers

The facts at the top name the service's domain, its owner, the contract it runs,
its capabilities and the sources that saw it. The operational summary shows
compliance, how certainly each target is matched to a revision, how recently it
was observed and how the declared dependencies compare with observed traffic.
The sections below list findings, observed traffic, operational targets,
delivery, revisions, expected dependencies, dependents, signatures, policies,
configuration, recent evidence and the limitations that make any of it
uncertain.

## Owner and contract

The Owner fact reads **No declared owner** when a readable contract revision
exists and none of them names an owner. It reads **Owner unknown** when Pacto
could not read any of the service's contracts and no operational target reports
the contract it runs, so Pacto cannot tell whether an owner is declared. The
fleet counts both cases as unowned, and both link to this service's `unowned`
attention item. For an unknown owner that item says Pacto could not read the
service's contracts and asks you to make one readable, not to declare an owner. A declared owner links to the revision that declares it, under
**Declared in**, where the owner's contacts are listed. The Contract fact links
the newest revision in use, or the newest known revision when nothing runs the
service.

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
reach the cluster it shows the error with a retry.

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
- **Signature** shows the path's signature check. Verified names the signer
  the path's own contract declares, which no trust config checked;
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
from a registry, it lists the OCI referrers attached to the revision's digest
and verifies its Sigstore signature against the signer the revision's own
contract declares in `publish.sign`. Revisions read from a local directory, the
cache or a catalog read **Not checked**.

| Status | When |
|---|---|
| `verified` | A Sigstore bundle verifies against the keyless identity and issuer the contract's `publish.sign` declares |
| `unverified` | A signature was read and does not verify against that identity and issuer. The page shows the declared identity as the **Expected signer**, because who signed it is not known |
| `unsigned` | No Sigstore bundle is attached to the digest, or the attached bundles hold no signature |
| `unknown` | Pacto could not decide: the registry could not list the referrers or returned only their first page, the revision has no digest, the contract names no signer or signs with a key or KMS key, the trusted root could not be fetched, or the checks ran out of time. The reason is shown |

`verified` means the identity the contract itself declares signed the bundle.
It is a signature check and makes no trust decision: anyone who can push a
bundle can declare their own identity, so the signer is always shown beside it.
Delivery pins who may sign a bundle in Flux's `OCIRepository` `spec.verify`. A
key or KMS signer reads `unknown` because the bundle carries no public key and
the dashboard never calls a contract's KMS. Legacy cosign `.sig` tags are not
read. GHCR works through the referrers tag schema.

Each result is cached for 5 minutes, and a failed check for 1 minute. The
checks of one refresh share a 20 second budget. `pacto fleet` runs the same check on
each registry reference given with `--oci`. The result is the `provenance` field of each revision in
`pacto_fleet_get` and in `/api/fleet/entities/revision?key=<key>`.
