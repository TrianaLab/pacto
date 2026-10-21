# Pacto delivery: history, blame and verification

With the [recorder](delivery-records.md) on, what happened on a Stage is read
from its signed records in the registry, not from the Operations that happen
to be left in the cluster. The CLI, the MCP tools and the dashboard all read
them the same way.

## Tell Pacto which keys to trust

Readers check every record's signature against a trust config: the
recorder's PEM public key, or a YAML file in the format of
[`recorder.trust`](delivery-records.md#trust-and-key-rotation). Pass it with
`--trust` (or set `PACTO_DELIVERY_TRUST`). For `pacto mcp --delivery` and
`pacto dashboard` the flag is `--delivery-trust`.

Without a trust config the records are still read, but no signature is
checked, and every answer says so and is partial.

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

With records, the operations that ended but are not recorded yet are listed
too, marked as not recorded. `problems` lists what the records show is wrong:
missing numbers, two records with one number, a record that does not chain
from the one before, a gap record, or a change that started from a version
the previous record did not leave. Pacto reports them and resolves none.
`unreadable` lists what could not be read or verified.

When completeness is `partial`, a change missing from the answer may still
have happened. Never read it as proof that nothing happened.

## Blame and undo

```bash
pacto delivery blame -n prod --stage payments
pacto delivery undo -n prod <operation>
```

`blame` names the newest change that moved the Stage to the version it runs
(or to `--digest`): who asked, for whom, why, who approved and what every
check said. No change on a partial history means unknown, never nobody.

`undo` proposes nothing. It returns the plan that would undo an operation and
the command that would propose it: a rollback when the Stage still runs that
change, a promote back to the version it replaced when something changed
since (every check runs again), or nothing when the operation moved nothing.
For an Operation already deleted, add `--stage`.

## Precedent

`pacto delivery plan` lists what the records attached to the new version say
happened to it on other Stages, newest first. A version that failed in test
shows up when you plan it for prod.

## Verify records

```bash
pacto delivery record verify -n prod --stage payments --trust recorder.pub
```

It checks every record's signature and the chain from the first record to the
newest, and exits with an error if anything is wrong. It also prints a command
that checks one record without Pacto, with [oras](https://oras.land) and
[cosign](https://github.com/sigstore/cosign):

```bash
REPO=registry.example/pacto/streams
oras blob fetch --output record.json \
  "$REPO@$(oras manifest fetch "$REPO@<record manifest>" | jq -r '.layers[0].digest')"
cosign verify-blob-attestation --bundle record.json --key recorder.pub \
  --type pacto.dev/operation/v1 --insecure-ignore-tlog \
  --digest <anchor digest without sha256:> --digestAlg sha256
```

`oras discover` on a version lists the records attached to it. The record
itself is a DSSE envelope: `jq -r .dsseEnvelope.payload record.json | base64 -d`
prints the statement.

## Registry support and retention

The registry must serve the OCI Referrers API natively. GitHub's registry and
released `distribution` do not, so there are no records there and history
comes from the cluster. A registry that deletes old manifests deletes old
records with them: history then starts later and is partial.
