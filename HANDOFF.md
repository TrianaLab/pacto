# HANDOFF from the logic track (feat/pd-logic)

Requests outside the logic area, for the integrator and the named tracks.

## 0. Area note

`pkg/deliveryrecord` (where delivery records live in a registry) is in no track's table. The
logic track changed it, because it is the records layer the refusal data source needs: a
shared fetch helper and the new `refusal.go`. Please confirm when you merge.

## 1. internal/app onto the pure answers (core area, ready patch)

`pkg/delivery` now holds the pure assembly that `internal/app` keeps privately:
`NowOf`, `ExplainOperation`, `Summarize`, `RerunHint`, `AssembleHistory` (with `RecordsRead`),
`StatusEntry`, `MissingSeqs`, `WithUnsignedMove`, `BlameOf`, `UndoOf` and `Undo.Planned`.
This closes the critic finding that explain, history and blame assembly live only in
client-go code the browser demo cannot import.

`/Users/eduardodiazasencio/repositories/trianalab/pacto/docs/superpowers/specs/2026-10-22-pd-logic-internal-app.patch`
(gitignored, beside the specs, so it never trips `git diff --check`) moves `internal/app` onto
them: `git apply <that path>` from the repo root after merging this branch. It deletes the
private copies (288 lines) and changes one test line (`summarize` -> `delivery.Summarize`).
Checked on this branch: with the patch applied, `go test ./internal/app/` passes at 100.0%
coverage with no other test change. If core changes those files first, regenerate it by
hand the same way: `history`, `Blame`, `Undo` and `Explain` call the `pkg/delivery`
functions; `streamRead` embeds `delivery.RecordsRead` and sets `Trust` to the trust
config digest.

## 2. Refused publishes: the agreed data source (core writes, dash reads)

A refusal never reaches the cluster, so the registry holds it, signed, like records.

Shape (`pkg/delivery/refusal.go`): an in-toto statement, predicate
`pacto.dev/publish-refusal/v1`, subject `contract` = the digest publish computed for the
refused bundle (never pushed). Predicate:

```json
{"service": "payments", "version": "2.4.3", "baseline": "2.4.2", "classification": "BREAKING",
 "requiredBump": "major", "message": "BREAKING since 2.4.2 needs a major bump", "at": "2026-10-22T10:00:00Z"}
```

Where (`pkg/deliveryrecord/refusal.go`): a referrer of a per-service anchor
(`RefusalAnchor(service)`, tagged `RefusalAnchorTag(service)`) in the service's contract
bundle repository. Every version of a service shares that repository, so both sides find it
from a contract reference alone.

- **core (`pkg/publish`, MCP `pacto_publish`)**: when the version rule refuses, build
  `delivery.NewRefusalStatement(bundleDigest, delivery.PublishRefusal{...})` (Classification =
  `d.Classification.String()`, RequiredBump, Message = the check message, At = now in RFC 3339),
  sign it with the publish signer (`delivery.SignRefusal`) and push it with
  `deliveryrecord.PushRefusal(ctx, repo, bundle)` on the contract bundle repository, under a
  timeout. Publish still exits with the refusal; a push that fails is a warning beside it,
  never a reason to publish.
- **dash (`pkg/dashboard`, `internal/cli/dashboard*.go`)**: for each service the Published
  view shows, take the repository of the contract reference of any digest its Stages know
  (`DeliveryAPI.Describe(...).ContractRef`, the part before `@`) and call
  `deliveryrecord.ListRefusals(ctx, repo, service, verify)`. It returns
  `deliveryrecord.RefusalListing` (JSON: `refusals[]` of `{id, manifest, statement}` newest
  first, at most `MaxRefusals` = 20, and `unreadable[]`). Suggested API: add to each service in
  `GET /api/delivery/published` a `refusals` object `{items, unreadable, verified, error}`,
  where `verified` is false when no publisher key was given and `error` says why the registry
  could not be read (never shown as "no refusals"). `verify` is `func(bundle []byte) error`;
  with no publisher trust, pass one that returns nil and label the list "signature not checked".
  Show version, required bump, baseline and time, for example "2.4.3 refused: needs a major
  bump since 2.4.2".

## 3. Docs track (`integrations/kubernetes/docs/delivery.md`, "When another service must move first")

The sentence "While a change waits, the promoter plans its order again, so a consumer that
has moved drops out and a read that timed out is tried again" should add: "and its team is
no longer asked: only the teams the change still breaks may approve it, and a team it newly
breaks is asked too". A refused publish is now also kept in the registry as a signed
refusal (once core wires section 2).

## 4. Kind harness (ci or kindfast track, `tests/acceptance/kind/delivery.sh` phase 5)

Optional but worth it: prove B6 on the cluster with the SAME held operation. Two consumers
that accept only the old major (orders and a second one); propose the provider, check
`.status.plan.floor.owners` names both teams; promote one consumer first WITHOUT cancelling;
`eventually` the held operation's `.status.plan.floor.owners` names only the other team, an
approval from the moved team is not consumed and one from the remaining team ships it. The
unit proof is `TestHeldOrderIsRefreshed` in `integrations/kubernetes/internal/delivery`.

## 5. Story and demos tracks

- The story track's HANDOFF item 4 is resolved: the operation held in B5 now re-asks only
  orders once checkout 1.9 runs, so the Kubernetes demo plays B6 and B7 without cancelling
  and proposing 3.0.0 again. Why it works on Kind: each Pacto adds the one contract its
  status holds to the snapshot, and the operator updates that status when Flux applies
  checkout 1.9, so checkout 1.8 leaves the snapshot and the next re-plan drops checkout.
- The browser demo can assemble explain, history, blame and undo with the `pkg/delivery`
  functions in section 1 over its fixed data (wasm-safe: no k8s.io or sigstore in
  `go list -deps ./pkg/delivery`), and show a refusal with `delivery.PublishRefusal`
  labelled as fixture data.
