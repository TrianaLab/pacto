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
from the one before, a gap record or a change that started from a version
the previous record did not leave. Pacto reports them and resolves none.
A number the Stage gave out that has neither a record yet nor an
Operation left in the cluster is a problem too. An Operation whose record
exists but did not verify is marked "recorded, not verified", never "not
recorded yet". `unreadable` lists what could not be read or verified.

When completeness is `partial`, a change missing from the answer may still
have happened. Never read it as proof that nothing happened.

### Every Stage at once

```bash
pacto delivery history --all --trust recorder.pub
pacto delivery record verify --all --trust recorder.pub
```

`--all` walks every Stage in every namespace you can list, or only those in
the namespace you pass with `-n`. If you may not list Stages in every
namespace at once, it lists the namespaces, reads the Stages in each one you
can and lists the others under `unreadable`, so the answer is `partial`. If
you may not list namespaces either, pass `-n`. It reads up to 500 Stages, eight at a time,
for at most two minutes. The answer has one `asOf` and is `complete` only
when every Stage was read and every history is complete. A Stage it could not
read is listed under `unreadable`. `record verify --all` fails unless every
stream verifies, and a Stage with no records does not. The MCP tools
`pacto_delivery_history` and `pacto_delivery_record_verify` do the same when
you leave `stage` empty. The dashboard's History view lists every Stage you
can see.

## Blame and undo

```bash
pacto delivery blame -n prod --stage payments
pacto delivery undo -n prod <operation>
```

`blame` names the newest change that moved the Stage to the version it runs
(or to `--digest`): who asked, for whom, why, who approved and what every
check said. No change on a partial history means unknown, never nobody.
When the change comes from an Operation's status rather than a signed record,
because it is not recorded yet or its record could not vouch for it, `unsigned`
names the parts nothing signed vouches for and the answer is `partial`.

`undo` proposes nothing. It returns the plan that would undo an operation and
the command that would propose it: a rollback when the Stage still runs that
change, a promote back to the version it replaced when something changed
since (every check runs again), or nothing when the operation moved nothing.
For an Operation already deleted, add `--stage`. A deleted Operation cannot be
rolled back even when the Stage still runs its change: undo then plans a
promote back to the version it replaced.

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

A keyless recorder has no key file. For a stream it signs, the command puts
`--trusted-root trusted_root.json --certificate-identity <identity>
--certificate-oidc-issuer <issuer> --use-signed-timestamps` in place of
`--key recorder.pub`. Save the `trustedRoot` of its trust config entry as
`trusted_root.json`.

`oras discover` on a version lists the records attached to it. The record
itself is a DSSE envelope: `jq -r .dsseEnvelope.payload record.json | base64 -d`
prints the statement.

## Registry support and retention

The registry must serve the OCI Referrers API natively. GitHub's registry and
released `distribution` do not, so there are no records there and history
comes from the cluster. A registry that deletes old manifests deletes old
records with them: history then starts later and is partial.
