---
# Contributor-internal. It is thorough, so it keeps outranking the reader
# pages on general queries; halve it rather than thin the page.
search:
  boost: 0.5
---

# Abandoned release transactions

Two transactions in the [release pipeline](releases.md) published part-way and
stopped. Their versions are not installable, and these are the records of what
each one left behind.

## Abandoned transaction `522e9507410f16fc` (3.2.0 / 5.2.0)

This transaction published four units and then failed. v3.2.0 and v5.2.0 are
**tagged and resolvable through the Go module proxy but were never released**:
neither has a GitHub Release, and `releases/latest` — which the CLI's update
check and `scripts/get-pacto.sh` both read — correctly skips them. Two rules
follow, and they are permanent:

- **Never create a back-dated GitHub Release for either version.** GitHub picks
  `latest` by creation time, so it would advertise them as newer than whatever
  has shipped since.
- **Never expect the transaction to resume.** Every publisher checks out
  `ref: source_sha`, and that commit cannot publish. It was superseded by
  3.2.1 / 5.2.1. Its ledger entries are permanent, so a recovery dispatch for
  that transaction id stays armed and still refuses to double-publish the four
  units that completed.

## Abandoned transaction `3bf445d36fd2c3fc` (5.2.2)

This one published **nothing** — no module tag, no operator image, no chart — and
reported success. Run 32647472671 on `be20e3b3` (#319) is green in the Actions list.
`detect` resolved correctly (`release=true`, the four Kubernetes units), `core-ready`
succeeded, and then `ledger-init` and every publisher below it skipped.

The cause was skip propagation through `needs`. A Kubernetes-only transaction has no
core unit, so `core-tag` skips by design. `core-ready` carried `always()` and ran, but
`always()` exempts only the job that declares it: the skip still reached everything
downstream of `core-tag`, and `ledger-init` — which did not declare it — skipped with
its `if:` satisfied and both of its `needs` green. A skipped job is not a failed job,
so the run's conclusion stayed `success`. Fixed in #361 and #362.

5.2.2 is therefore unlike 3.2.0 / 5.2.0: there is no tag, so nothing resolves through
the module proxy either, and no artifact exists to be adopted or conflicted with. It
was superseded by **5.2.3**, which published in full. Nothing needs recovering, and a
recovery dispatch for this transaction id would rebuild from a commit whose versions
have since been republished — do not send one.
