# Delivery reference: history, blame and verification

With the [recorder](delivery-records.md) on, the CLI, the MCP tools and the
dashboard read what happened on a Stage from its signed records, not from the
Operations left in the cluster.

## Tell Pacto which keys to trust

Readers check every signature against a trust config: the recorder's PEM
public key, or a YAML file in the format of
[`recorder.trust`](delivery-records.md#trust-and-key-rotation). Pass it with
`--trust` or `PACTO_DELIVERY_TRUST`, and to `pacto mcp --delivery` and
`pacto dashboard` with `--delivery-trust`. Without one, records are read
unchecked and every answer says so and is partial.

## History

```bash
pacto delivery history -n prod --stage payments --trust recorder.pub
```

Every answer says when it was read (`asOf`), where it came from (`source`)
and whether it is the whole story (`completeness`):

| Source | Completeness |
|---|---|
| `records` | `complete` when every record verifies and the chain is whole from the first record to the newest; `partial` otherwise |
| `status` | always `partial`: the recorder is off or the registry could not be read, so only the Operations still in the cluster are listed |

Operations that ended but are not recorded yet are listed as not recorded,
and one whose record did not verify as "recorded, not verified". `problems`
lists what the records show is wrong, and Pacto resolves none of it: missing
numbers, two records with one number, a broken chain, a gap record, a change
that started from a version the previous record did not leave, or a number
with neither a record nor an Operation. `unreadable` lists what could not be
read or verified. When completeness is `partial`, a change missing from the
answer may still have happened.

### Every Stage at once

```bash
pacto delivery history --all --trust recorder.pub
pacto delivery record verify --all --trust recorder.pub
```

`--all` walks every Stage you can list, or those in the namespace you pass
with `-n`. If you may not list Stages across namespaces, it reads each
namespace you can and lists the rest under `unreadable`; if you may not list
namespaces either, pass `-n`. The run reads up to 500 Stages, eight at a time,
for at most two minutes, and lists what it had no time or room for under
`unreadable`. The answer is `complete` only when every Stage was read and every
history is complete. `record verify --all` fails unless every stream verifies;
a Stage with no records does not fail it. The MCP tools do the same when
`stage` is empty, and the dashboard's History view lists every Stage you can
see.

## Blame and undo

```bash
pacto delivery blame -n prod --stage payments
pacto delivery undo -n prod <operation>
```

`blame` names the newest change that moved the Stage to the version it runs
(or to `--digest`): who asked, for whom, why, who approved and what every
check said. No change on a partial history means unknown, never nobody. When
the answer comes from an Operation's status rather than a verified record,
`unsigned` names what nothing signed vouches for and the answer is `partial`.

`undo` proposes nothing. It returns the plan and the command that would undo
an operation: a rollback while the Stage still runs that change and the
Operation exists, otherwise a promote back to the version it replaced (every
check runs again), or nothing when the operation moved nothing. For a deleted
Operation, add `--stage`.

## Precedent

`pacto delivery plan` lists what the records attached to the new version say
happened to it on other Stages, newest first. A version that failed in test
shows up when you plan it for prod.

## Verify records

```bash
pacto delivery record verify -n prod --stage payments --trust recorder.pub
```

It checks every signature and the chain from the first record to the newest,
exits with an error if anything is wrong and prints a command that checks one
record without Pacto, with [oras](https://oras.land) and
[cosign](https://github.com/sigstore/cosign):

```bash
REPO=registry.example/pacto/streams
oras blob fetch --output record.json \
  "$REPO@$(oras manifest fetch "$REPO@<record manifest>" | jq -r '.layers[0].digest')"
cosign verify-blob-attestation --bundle record.json --key recorder.pub \
  --type pacto.dev/operation/v1 --insecure-ignore-tlog \
  --digest <anchor digest without sha256:> --digestAlg sha256
```

For a keyless recorder the command uses these flags instead of
`--key recorder.pub`, with the entry's `trustedRoot` saved as
`trusted_root.json`:

```bash
--trusted-root trusted_root.json --certificate-identity <identity> \
  --certificate-oidc-issuer <issuer> --use-signed-timestamps
```

`oras discover` on a version lists the records attached to it. The record
itself is a DSSE envelope: `jq -r .dsseEnvelope.payload record.json | base64 -d`
prints the statement.
