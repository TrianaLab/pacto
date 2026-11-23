# Sit on top of any IDP

Pacto sits on top of the internal developer platform your organization already
runs, public such as Backstage or Port, or your own in-house portal. The IDP
keeps its catalog, its deploy engine and its credentials. Pacto adds what its
relations lack, through three seams that need no Pacto code per IDP.

## What you add to the platform you run

On the services your IDP already lists, Pacto adds:

- **Context**: who owns each service, what it offers, which services it calls
  and how Pacto knows each fact (declared, observed or inferred).
- **Breaking-change impact**: the range each dependency accepts and whether the
  provider revisions Pacto reads from the registry satisfy it (the locked
  revision, or every published one), so `pacto impact` names the consumers a
  new version breaks. The versions a running target reaches show up in its
  conformance.
- **Delivery and IDP**: each service's runtime conformance and the
  provisioning reports your IDP sends back, read next to the contract.

## The three seams

| Seam | What it carries | Who signs it |
|---|---|---|
| [Contribution formats](write-a-source.md) | Services and calls the IDP knows, pushed as inferred records under `<registry>/pacto-inferred/<producer>` | The IDP, with its evidence key (`pacto push --sign-key`) |
| [The served route](backstage.md#serve-entities-from-the-dashboard) | `GET /api/backstage/entities`: every service with its provenance, ranges, range status, conformance, offering and producer | Nobody: the dashboard answers with what the reader's token may read |
| [Signed evidence](evidence.md) | Running targets (`WorkloadObserved`), [provisioning reports](provider-reports.md) (`ResourceProvisioned`) and [extension capabilities](evidence.md#extension-capabilities) | The IDP, under a grant in the Evidence Server's trust config |

The IDP uses each seam with `curl` and the `pacto` CLI. A seam an IDP does not
use costs nothing: every one of them is optional.

## Walk through a private IDP

`acme-portal` is a whole private IDP in one script. It contributes a service
the portal runs without a contract, reports a running `checkout` target named
`checkout-prod` with the dependency versions it reaches, reads the served
route and checks the ranges and conformance there, then reports that it
provisioned `checkout`'s database. It reads its endpoints from the environment
and needs `curl`, the `pacto` CLI and coreutils. Pacto's tests run this exact
script against a registry, an Evidence Server and a dashboard.

The script is a template. The service, versions and database it reports are
fixture facts, and it signs them as verified evidence: replace them with what
your portal knows before you run it against a real platform.

```bash
--8<-- "tests/anyidp/acme-portal.sh"
```

The first run only creates the signing key under `$KEYS` and stops, since the
Evidence Server cannot trust the key yet. Hand
`acme-portal__acme-portal-2026.pub` to the platform, which places it next to
the Evidence Server's trust config and adds this grant, then run the script
again. `$REGISTRY` stands for
your registry, such as `registry.acme.internal/acme`:

```yaml
--8<-- "tests/anyidp/trust.yaml"
```

The reader token stays in a file, so `ps` and `/proc` on the IDP host never
show it. `umask 077` creates the file readable by its owner only:

```bash
umask 077
printf 'Authorization: Bearer %s\n' \
  "$(kubectl -n pacto create token pacto-viewer --audience pacto-dashboard --duration 720h)" \
  > "$HOME/.acme-portal/pacto-reader"
export PACTO_TOKEN_FILE="$HOME/.acme-portal/pacto-reader"
```

Give the reader a cluster-wide grant: a narrowed reader sees fewer services,
and an IDP that mirrors the route would drop the rest
([who reads the route](backstage.md#serve-entities-from-the-dashboard)).

## What Pacto does with it

- The contributed `legacy-billing` reads `inferred` and `contributed-by:
  acme-portal`. Its calls never satisfy a range and never clear a gate. A
  person turns it into a contract with
  [`pacto declare`](publishing.md#declare-a-running-service), under their own
  identity.
- `checkout`'s database reads `Ready`, checked against the signed report. A
  consumer of the same offering that no producer reports on reads `Unknown`.
  Silence never reads as ready or compliant.
- The route answers over the reader's grants, so a hidden service or relation
  is only counted, never named.
- `acme-portal` shows as its own `producer/acme-portal` row in
  [Detected providers](operational-graph-providers.md#producers), with its own
  status and `asOf`.
- Pacto stores no IDP credential and writes nothing to the IDP. The IDP holds
  only a reader token and its own signing key.

## Further worked examples

- [Port Ocean](https://github.com/TrianaLab/pacto-plugins/tree/main/examples/port-ocean)
  maps the served route into Port blueprints with an Ocean custom integration.
- [Humanitec and Score](https://github.com/TrianaLab/pacto-plugins/tree/main/examples/humanitec-score)
  turns `score.yaml` files into contribution bundles and signs provisioning
  reports.

Both use the same seams as `acme-portal` and nothing else.

## Coming next

- Provisioning on an external IDP, where Pacto publishes a signed provision
  intent and the IDP reports back what it made, follows in a later release.
- Offerings an IDP contributes, inferred until a person accepts them, follow
  in a later release. Today a service names the offering it provides with
  `service.offering` in its contract ([platform offerings](platform-offerings.md)).
