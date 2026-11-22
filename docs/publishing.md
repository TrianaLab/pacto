# Publish a version

This page describes `pacto publish`. To take a new service to its
first release, follow [Pactify a service](pactify.md). In CI, publish every
changed service of a repository with
[`pacto ci --publish`](publish-from-ci.md), which runs the same engine;
[After a merge: publish](ci.md#after-a-merge-publish) sets it up.

`pacto publish` publishes what a contract's [`publish`](contract-reference/publish.md)
section declares: the contract bundle, the service's images and the manifests
or chart an applier deploys, all tagged with the service version. Pacto
derives a `publish` section or a `service.version` the contract leaves out
from the folder and the registry, as
[`pacto ci --publish`](publish-from-ci.md#what-pacto-derives) describes. Pacto pushes, signs and checks.

```bash
pacto publish --plan    # what is missing, and what would refuse it; writes nothing
pacto publish           # publish it
```

`publish` needs contract format 2.1, which earlier Pacto releases
cannot read. Before your first publish, make sure the services that depend
on yours run a Pacto and operator that read 2.1: see
[`publish`](contract-reference/publish.md#publish).

## What it checks

Pacto runs no tests. It refuses to publish when one of its own checks fails:

- the contract is invalid, or uses a local reference or breaks its `pacto.lock`;
- the contract does not declare `service.name` (`PUBLISH_NEEDS_SERVICE_NAME`).
  A name Pacto inferred from the folder is never published. An undeclared
  `service.version` is derived from the newest verified version in the
  registry ([the rule](publish-from-ci.md#the-derived-version)), while
  `pacto pack` and `pacto push` refuse it (`PUBLISH_NEEDS_SERVICE_VERSION`);
- a provider does not meet what a required dependency `requires`
  (`GUARANTEE_UNMET`). Pacto fetches each provider that a dependency declares
  `requires` for, as `pacto validate` does, and `--plan` shows the unmet
  guarantee as the failed `validation` check. An unmet guarantee of an optional
  dependency and a provider Pacto cannot fetch (`GUARANTEE_UNVERIFIED`) are
  warnings: the `validation` check passes and names them;
- a configuration reference that a check depends on cannot be fetched
  (`CONFIG_REF_UNRESOLVED`), as with an unreachable policy reference, or the
  values fail the referenced schema. `pacto push` refuses the same way. A
  `"2.0"` to `"2.3"` reference with no values anywhere only warns; see
  [Resolution and versions](contract-reference/configuration-and-policy.md#resolution-and-versions);
- the signing key file would end up inside the bundle, or the signer does
  not open: a missing key file, a wrong `COSIGN_PASSWORD`, an unreachable KMS
  key or, for keyless signing, no OIDC token refuses the plan, before
  anything is built;
- there is no source revision (run it in a git checkout, or pass `--revision`).
  Pacto reads the commit from `.git` itself, worktrees and packed refs
  included;
- the git checkout has uncommitted changes to tracked files, so the commit
  stamped and signed on every artifact would not hold what was built. Publish
  from a clean checkout. This check runs `git status`; `--revision` skips it,
  since you then name the revision yourself. Untracked files are not checked;
- a `dockerfile` build has no usable `docker buildx` builder, a `command`
  build's program is not an executable here, or a build tool Pacto detected
  is not installed;
- two artifacts, or an artifact and the bundle, share one repository;
- an SBOM is declared on a `dockerfile` build, which writes none, or sits in
  the contract directory without a `.pactoignore` line (`--plan` names it);
- the version is too small for the change.

The signer, the clean checkout and the builders are checked only for what is
still to write: a builder for each artifact not yet published, the signer and
`git status` when anything is. Running publish again for a version already
published (to `--propose` it, say) needs none of them.

A version too small for the change is also kept as a signed refusal in the
contract bundle repository, signed with the publish key, so the dashboard's
**Published** view shows it with the bump it needed. The refusal record leaves
the outcome unchanged: if the registry rejects the record, publish reports that beside the refusal.

A release candidate's fixes ship as the version it was a candidate for: after
`2.0.0-rc.1`, `2.0.0-rc.2` and `2.0.0` are accepted whatever changed. The change
since the last final release must still fit the version: after `1.0.0` and
`1.1.0-rc.1`, a breaking change cannot ship as `1.1.0`. The version
rule compares the contract with the highest version already
published below it, using the same classification as [`pacto diff`](contract-reference/diff.md):

| Change since the last version | Smallest version allowed |
|---|---|
| Breaking | a new major version (before 1.0, a new minor) |
| Potentially breaking: Pacto cannot tell | a new minor version |
| Not breaking | any higher version |

`pacto diff` prints the same rule as `Required bump`.

## Never overwritten

For every artifact the plan says what the registry holds for its tag:

| State | Meaning | Publish does |
|---|---|---|
| `absent` | The tag does not exist | builds, pushes and tags it |
| `identical` | The tag holds exactly what Pacto computed (the bundle) | nothing |
| `adopt` | Its version and revision annotations match this publish | reuses it if it is signed by the configured identity; refuses otherwise |
| `conflict` | The tag holds something else | refuses; nothing is overwritten |
| `unreadable` | The registry could not be read | refuses; unreadable is never absent |

Annotations are not signed, so anyone who can push can write them. Pacto
therefore never signs what it adopts: a tag it wrote was signed before it was
tagged, and one without that signature is someone else's.

A run that stopped halfway finishes when you run it again from the same
commit: the plan adopts what the first run pushed and writes only what is
missing. Run it from another commit and every tag the first run wrote is a
conflict, so that version is spent: publish a higher `service.version`.

The deploy artifact is tagged last: once its tag exists, everything it points
at exists and is signed. The other tags, the bundle's included, may already
exist when a run stops, but no Stage takes a version without its deploy
artifact. Each tag is read again just before it is written, which narrows the
window in which two runs racing on one version could both write. A registry has no
compare-and-swap on tags, so the window stays open: give each version one
publisher.

## What gets published

Each build gets `PACTO_OUT`, `PACTO_VERSION`, `PACTO_REVISION` and, for every
artifact built before it, `PACTO_DIGEST_<NAME>` and `PACTO_REF_<NAME>`, so a
manifests build can pin the image it deploys by digest. Pacto then:

1. pushes each artifact by digest, with `org.opencontainers.image.version` and
   `org.opencontainers.image.revision` on its manifest, and in CI
   `org.opencontainers.image.source`: `$GITHUB_SERVER_URL/$GITHUB_REPOSITORY`
   on GitHub Actions, `$CI_PROJECT_URL` on GitLab CI. The contract bundle
   carries none, so its digest stays the same wherever it is built;
2. annotates the deploy artifact, the manifests or chart any applier
   deploys, with `delivery.pacto.trianalab.io/contract`, which names the
   digest-pinned contract bundle;
3. attaches the SBOM file the build wrote to each image that declares one, and
   signs it, unless one signed by the configured identity is already there.
   Pacto never makes an SBOM, and refuses a file older than the build;
4. signs every digest, unless a valid signature by the configured
   key, KMS key or keyless identity is already there, then verifies the new
   signature against that identity. A keyless signature carries whatever
   identity the CI token had, so a mismatch fails here, before any tag is
   written, so Pacto never publishes a version no Stage would take;
5. pushes the bundle, attaches to it a signed record of every image the
   deploy pins (`application/vnd.pacto.images.v1+json`: each image's
   repository, digest and whether Pacto built it), and writes the tags.

The bundled `pacto.yaml` always declares `pactoVersion`. When the contract
leaves it out, `pacto pack`, `pacto push` and `pacto publish` write the newest
version the CLI supports into it. `pacto ci` also raises an older
`pactoVersion` in the bundle when that keeps the contract's meaning (see
[Older contracts](ci.md#older-contracts)).
Readers rely on it: a bundle pulled from a registry, or an inline contract in a
Pacto resource, without `pactoVersion`, `service.name` or `service.version` is
rejected. The operational graph reports it as `SOURCE_RECORD_INVALID` and the
operator as `ContractUndeclared`.

It needs `docker buildx` with a builder that can write an OCI layout (the
`docker-container` driver can) for `dockerfile` builds, and `git` to check that
the checkout is clean unless you pass `--revision`. It needs no `syft` or
`cosign`. With no declared build, it runs the build tool the folder shows
(Skaffold, ko, Jib, buildpacks or a Dockerfile) directly, never through a
shell, and assembles the deploy artifact itself; see
[How the image is built](publish-from-ci.md#how-the-image-is-built).
`publish.artifacts[].build` is deprecated in `pactoVersion: "2.5"`.
Registry credentials and plain-HTTP registries come from the same settings as
`pacto push`.

## Signing

Pacto signs with the Sigstore library and needs no `cosign` binary. Each
signature is a Sigstore bundle attached to the digest as a referrer, the way
cosign 3 writes one, so `cosign verify` and Flux's `spec.verify` both check it.

| `sign` | What signs | Notes |
|---|---|---|
| none, or no `publish` section | A short-lived certificate for the CI identity the OIDC token names | The identity rule and GitLab's `id_tokens` are in [Signing](publish-from-ci.md#signing). `--sign-key` signs a derived section with a key |
| `key: cosign.key` | A PEM private key file | A cosign key is decrypted with `COSIGN_PASSWORD` |
| `kms: awskms:///alias/ci` | A KMS key | `awskms://`, `gcpkms://`, `azurekms://` or `hashivault://`, with that cloud's usual credentials. The key must be ECDSA P-256, Ed25519 or RSA PKCS#1 v1.5 with SHA-256: Pacto refuses RSA-PSS and SHA-384 or SHA-512 keys. The released `pacto` binaries leave cloud KMS out to stay small: build one with `go install -tags kms github.com/trianalab/pacto/v4/cmd/pacto@<version>`, or one provider's tag (`kms_aws`, `kms_gcp`, `kms_azure`, `kms_hashivault`). The error names the build to use |
| `keyless: true` | A short-lived certificate from public-good Fulcio | The OIDC token comes from `SIGSTORE_ID_TOKEN` or GitHub Actions (`id-token: write`) |

A signature goes to the public transparency log unless `transparencyLog:
false`. Keyless signing needs the log: its certificate lives ten minutes, so
it verifies only at the time the log saw it, and Pacto refuses keyless with
`transparencyLog: false`. Pacto verifies a keyless signature against the
`identity` and `issuer` you configure, or the ones the token names when you
configure none, under the trusted root Sigstore
publishes through TUF. `publish.sign` only says how publish signs: who may
publish a service is the installation's decision, checked against a
publisher rule, a person's pin or a signed policy
([which publishers Pacto accepts](integrations/kubernetes/delivery-security.md#which-publishers-pacto-accepts)).
An operator signature never declares a contract
([`run.pacto.contract.provenance: inferred`](inferred-contracts.md#the-signer)).
The repository a keyless CI certificate attests must match the artifact's
`org.opencontainers.image.source`. The dashboard and `pacto_fleet_get` show whether a
published version's signature verifies; see [Bundle signatures and
referrers](dashboard-service-page.md#bundle-signatures-and-referrers).

## The organization index entry

Every published version also writes one entry to the
[organization index](impact-consumers.md#consumers-from-the-organization-index),
`<registry>/pacto-index:<hash>-<version>`. It exposes the service name,
version, bundle digest, every dependency reference and the publisher it claims.
An entry is never overwritten. A failed write warns and the publish
still succeeds; publishing the current version again writes a missing entry.

- `--index` names another `<registry>/pacto-index`, with no tag or digest, and
  `--index none` writes none.
- `pacto push` under a tag other than the version writes none.

## Fields declared in the registry

- **Kept.** `pacto ci --publish` keeps what a person
  [declared](#declare-a-running-service) in each later version.
- **Baseline.** The declaration counts as the baseline the next version is
  derived from. The plan lines are in
  [Publish from CI](publish-from-ci.md#fields-carried-from-the-registry).
- **What is carried.** The declared `service.owner` and each range the
  declaration accepted. A whole dependency entry is carried only while
  discovery still finds that dependency.
- **The repository wins.** A key the repository's `pacto.yaml` writes is never
  carried, an explicit `compatibility: unknown` included.
- **Owner.** The owner is carried only when the
  [owner rule](owner-setting.md) accepts the declaration, which needs an owner
  policy CI can read. Otherwise the plan prints
  `owner: billing, declared in registry (rev sha256:7c1d2e3f4a5b), not carried: no owner policy readable from CI: <why>`.
  Only a CI publish or a judged declaration sets the owner the rule starts
  from: a release pushed with registry write alone never does.
- **Ranges.** A range is carried only when an owner of the service signed the
  declaration.
- **Signers.** The installation signer policy says whose declarations count.
  With none, a person public-good Sigstore vouches for counts, and the plan
  notes `no installation signer policy was read; declarations judged with public-good issuers`.
- **Record.** The published record cites the declaration in
  [`inputs.declaration`](contract-reference/inferred-record.md).
- **Dropped.** A declaration that is gone or no longer counts is dropped with
  its reason, and the version publishes with the repository's values.
- **Unknown stops.** A declaration, or the owner history behind it, that
  cannot be checked under a configured trusted root stops that service's
  publish.
- **Plain publish.** `pacto publish` carries nothing, and says which declared
  fields the new version leaves out.

## In CI and from agents

CI calls [`pacto ci --publish`](publish-from-ci.md) as one step after the
tests pass, on the merged commit. An agent calls `pacto publish`
through MCP: `pacto mcp --publish` adds `pacto_publish_plan` and
`pacto_publish` beside the authoring tools, over stdio only.

- **Carry-forward.** `pacto ci --publish` reads the signed
  [acceptances](ci.md#accept-proposed-ranges) attached to the version before
  and re-reads the ones its inferred record cites. It pins each counted range
  into the published `pacto.yaml`, unless the repository's `pacto.yaml`
  declares that field, and cites every carried record in
  [`inputs.acceptances`](contract-reference/inferred-record.md). A record that
  no longer verifies stops carrying. A plain `pacto publish` carries none.
  Fields a person declared are carried the same way: see
  [Fields declared in the registry](#fields-declared-in-the-registry).

## Declare a running service

`pacto declare <workload> --registry <registry>` publishes a contract for a
running workload, without a repository or a build.

- **Source.** It drafts the contract from the cluster with your kubeconfig
  credentials, as
  [`pacto init --cluster`](bootstrap-contracts.md#draft-from-a-running-cluster)
  does, or reads `--dir`, a folder that command wrote.
- **Plan.** Without `--yes` it prints the plan and writes nothing; the plan
  ends with its intent digest, and `--intent <digest>` refuses a run whose
  plan changed since you reviewed it.
- **Contract only.** The bundle holds `pacto.yaml`, the files it references
  and `pacto.inferred.yaml`. Nothing is built and no deploy tag is written:
  the plan reads `deploy: none`, or the deploy artifact of the published
  baseline, which keeps pointing at the baseline.
- **Version.** A first declaration is `0.1.0`. With a published baseline the
  version is the baseline raised by the change, as `pacto ci` derives it, and
  an unchanged declaration publishes nothing. A baseline below
  `pactoVersion: "2.5"` is refused until you run `pacto migrate`.
- **Who signs.** Only your interactive Sigstore login, in a browser or with
  `--device`, publishes a declared version. A key, an ambient OIDC token such
  as `SIGSTORE_ID_TOKEN`, a CI token or a workload identity writes the next
  `draft-<n>` tag instead. The plan warns that a keyless signature records your
  email in the public Rekor log.
- **Ranges.** A range the cluster read proposes stays `unknown` until you name
  its dependency with `--accept`. The record lists each accepted range.
- **Owner.** Setting, changing or removing `service.owner` takes a person who
  belongs to the current owner, declared or attested, or a default approver;
  anyone else is refused before anything is pushed. An owner read from the
  workload's team label stays inferred. An unsigned version pushed without
  `pacto declare` keeps its owner: registry write access is the trust
  boundary for unsigned pushes. See
  [Who may set a service owner](owner-setting.md).
- **Drafts.** `pacto declare <workload> --from-draft draft-<n> --yes` accepts a
  draft an agent or CI wrote: the workload is read again under your
  credentials, every declared change the read and the baseline do not back is
  flagged and you sign. `--draft` writes your own draft, unsigned and with no
  login, for someone to accept later. Every plan lists the drafts waiting.
- **Verify.** `pacto pull --verify-identity <email> --verify-issuer <issuer>`
  pulls a bundle only when that person signed it; `--verify-key` checks a key
  instead. The record's `declaration` block is in the
  [inferred record](contract-reference/inferred-record.md#declaration).
- **What the fleet shows after you declare.** Once a registry read verifies
  your signature and the installation signer rule allows you, the fleet
  shows one service: the inferred service the declaration names is handed
  over to the declared one, and each field says whether it is declared,
  accepted by you or inferred. A Pacto target that runs as the workload wins.
  See
  [When a declaration replaces an inferred service](operational-graph-inferred.md#when-a-declaration-replaces-an-inferred-service).

### Open a pull request

- **What it does.** `pacto declare invoices --pr --yes` opens a pull request
  with the declaration in the service's own repository. After the bundle is
  written, declared or draft, its exact `pacto.yaml` and
  `pacto.inferred.yaml` go into the service's folder in one commit on a new
  branch `pacto/declare-<service>-<version>`. An unchanged declaration
  exports the baseline bundle's own files.
- **Plan first.** Without `--yes` the plan adds the repository and pull
  request lines, checks the confirmation and the token, and sends nothing to
  the Git host.
- **Where it runs.** `--pr` runs where your Git token is: the CLI and a stdio
  MCP server; the dashboard and the HTTP MCP server show the command.

```console
repository: github.com/acme/invoices (signed provenance, workflow ci.yml@refs/heads/main), folder: deploy/ (GitOps source)
pull request: github https://github.com/acme/invoices, branch pacto/declare-invoices-1.5.0, files deploy/pacto.yaml and deploy/pacto.inferred.yaml, with your GITHUB_TOKEN
```

- **Where the repository comes from.** Every plan names it and how Pacto
  found it, kept in
  [`declaration.repository`](contract-reference/inferred-record.md#declaration).
  The first source that answers wins:

1. **Signed provenance.** A verified keyless signature or signed SLSA
   provenance attestation on the baseline bundle, then on the running image,
   whose Fulcio certificate names the source repository. Pacto reads Sigstore
   bundle referrers through the Referrers API or its own `pacto-sig` tags.
   Legacy cosign `.sig` tags and the shared `sha256-` referrers-tag index are
   not read, so a GitHub attestation on a registry without the Referrers API
   gives nothing and the plan says why. No allowlist is needed, and the
   repository it names never makes the signer a publisher or clears a gate.
2. **GitOps source.** The Argo CD Application or Flux `GitRepository` that
   deploys the workload, with its path as the folder.
3. **Image label.** The running image's `org.opencontainers.image.source`.
4. **Repository map.** A `--repo-map` file, read only with `--pr`: YAML keyed
   by service name, decoded strictly, at most 1 MiB, such as
   `invoices: {repository: https://github.com/acme/invoices, folder: deploy}`.

- **Your override.** `--repository` and `--folder` win over every source.
  The folder defaults to the repository root when no source names one.
- **Unknown stays unknown.** Several signed or GitOps repositories that
  disagree give no answer. With no answer the record has no `repository`, the
  plan lists why each source gave nothing and `--pr` is refused.
- **Confirmation.** An image label is unsigned: `--pr` uses it only when you
  pass the same `--repository` or the map names it. A repository an agent
  passes, as `repository` or through `repo_map`, steers a pull request only
  when signed provenance or a GitOps source names it too. A public repository
  needs your own `--repository`, since the record names the workload, its
  namespace, external hosts and config key names.
- **A failed pull request** keeps what was published: rerun with
  `--pr --yes`.
- **Review evidence.** CI recomputes `pacto.inferred.yaml` on its next
  publish, and the declared values travel through the registry
  ([Fields declared in the registry](#fields-declared-in-the-registry)).

- **Git host.** Pacto finds it from the URL, or from `--git-host`:

| Host kind | Detected from | Token variables |
|---|---|---|
| `github` | `github.com`, `GH_HOST` | `GITHUB_TOKEN` or `GH_TOKEN` for `github.com`; `GH_ENTERPRISE_TOKEN` for `GH_HOST` |
| `gitlab` | `gitlab.com`, `GITLAB_HOST` | `GITLAB_TOKEN` for `GITLAB_HOST`, default `gitlab.com` |
| `bitbucket` (Cloud) | `bitbucket.org` | `BITBUCKET_TOKEN` |
| `gitea` (Gitea 1.20 or later and Forgejo) | `codeberg.org`, `GITEA_HOST` | `GITEA_TOKEN` for `GITEA_HOST` |

- **Token.** A variable's token goes only to the host it names. Otherwise
  Pacto asks `git credential fill` without a prompt and never stores a
  credential.
- **Who opens it.** The token's user, whose login the CLI prints.
- **Transport.** Plain `http://` is refused except on loopback, and a
  redirect is refused so the token never follows it.
- **No fork.** A token that cannot push is refused with `PullRequestFailed`
  and the host's reason.

## Hand-off to delivery

With [Pacto delivery](integrations/kubernetes/delivery.md) installed, a Stage
that watches the deploy artifact's repository (`candidates.registry`), or whose
candidate `OCIRepository` follows it, sees each new version without an extra
step, and `--propose <namespace>/<stage>` proposes it on a Stage that does not
promote by itself. A Stage promotes only versions published this way: the
registry watch or Flux checks the signature and the promoter checks the
contract annotation. So `--propose` first waits until the Stage records the
version as published, which happens once the watch or Flux reads the new tag
and accepts its signer. It waits up to `--propose-timeout` (10 minutes by default), then
gives up and says so.

A CI service account is an agent to Pacto delivery, so its `--propose` must
also name the person who asked for the change with `--on-behalf-of`, as the
Kubernetes username the cluster authenticates them as, any OIDC prefix
included. A pull request author's Git login is a different name: map it to
that username first, or the person's own approval goes unrecognised. That
person may not approve the change, and the name is asserted, never verified
([agents](integrations/kubernetes/delivery-agents.md#an-agent-names-the-person-it-acts-for)).

To take a version back out of an environment, see
[Roll back by hand](pactify-decide.md#roll-back-by-hand).
