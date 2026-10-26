# Delivery reference: record keys

## Trust and key rotation

Readers and the recorder check every signature against a trust config. To
rotate, sign with the new key and keep the old one listed:

```yaml
delivery:
  recorder:
    trust: |
      keys:
        - name: old-recorder
          publicKey: |
            -----BEGIN PUBLIC KEY-----
            ...
            -----END PUBLIC KEY-----
          streams: ["*/prod/*"]
```

`streams` are patterns over stream ids (`<cluster>/<namespace>/<stage>`), so a
dev key cannot sign prod history. A keyless entry names `identity`, `issuer`
and the required `trustedRoot`. The recorder's own key is always trusted. List
the old key before the recorder signs with a new one, also when a KMS alias
moves. Otherwise the old records stop verifying and the Stage freezes. Pass the same file (or the recorder's PEM
public key) to the CLI with `--trust` or `PACTO_DELIVERY_TRUST`, and to
`pacto mcp --delivery` and `pacto dashboard` with `--delivery-trust`. Without
it, records are read unchecked and every answer says it is partial.

## Revoke a key

When a key leaks, rotate the recorder to a new key first. Then keep the old
key listed with `untilRecord`, the id of the last record it signed on that
stream, and `untilSeq`, that record's seq. The old key then verifies only that
record and those it chains from, so history stays readable. Anything else it
signs is skipped as untrusted, whether a record after `untilSeq` or a forgery
backdated to a seq it already used. So a forgery neither extends nor forks the
stream and the Stage does not freeze:

```yaml
keys:
  - name: leaked
    publicKey: |
      -----BEGIN PUBLIC KEY-----
      ...
      -----END PUBLIC KEY-----
    streams: ["<cluster>/prod/payments"]
    untilSeq: 41
    untilRecord: 3f9c...e1 # the record at seq 41
```

Seqs count per stream, so give a key that signed several streams one entry
per stream. The last seq and record of each are in the Stage's
`status.stream.recordedSeq` and `status.stream.head`, or in
`pacto delivery record verify`. Set both fields or neither. A read of one
version's records, not the whole stream, cannot walk the chain, so it shows
what the old key signed as not verified. Followers need the same trust
config: one that does not trust the new key sees the stream end at the last
record it can verify.

Removing the key instead makes its records unverifiable: the recorder stops
on the missing head and every follower sees a hole.
