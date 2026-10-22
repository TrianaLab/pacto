# HANDOFF from the docs track (feat/pd-docs, step 1 of 2)

## 1. Requests outside the docs track

1. `release/scripts/docs_check.py`, `check_coordinates()` (about line 548): the
   tuple `("delivery.md", "delivery-chart")` now checks a page with no install
   command. The delivery chart install moved to
   `integrations/kubernetes/docs/delivery-setup.md` and `docs/pactify.md` (both
   pull in `generated/_delivery-install-command.md`). Point the check at
   `delivery-setup.md`, and add `docs/pactify.md` (it lives under `docs/`, not
   `K8S/docs`). Why: otherwise a hand-written chart coordinate or `--version` on
   either page goes unchecked. Owner: integrator or ci.
2. No other file outside `docs/**`, `integrations/kubernetes/docs/**` and
   `mkdocs.yml` needs a change. The chart's `README.md.gotmpl` and `Chart.yaml`
   link to `/integrations/kubernetes/delivery/`, which still exists (now the
   "Delivery" front door).

## 2. Sentences to re-check once the smarter `pacto init` lands (core track)

Written from ledger section B, not from code. Every one names behaviour or a
flag that does not exist yet on b32da8b5.

`docs/pactify.md`:

- "From the root of your repository: `pacto init --plan`" (that init takes no
  name argument in an existing repository and runs in the repository root).
- "`pacto init` reads what is already there, such as the Dockerfile and any
  Kubernetes manifests, and fills in a contract from it, including how the
  service is published. With `--plan` it shows exactly what it would add and
  changes nothing."
- "The plan also shows what you get before you apply anything: the services
  yours depends on, the services that depend on yours and what Pacto could not
  work out, marked Unknown." (Does it find dependents, or only dependencies?)
- "This writes `pacto.yaml`. Running it again is safe: it adds what is missing
  and never overwrites your edits."
- "Check the contract's `publish` section first: it says where each piece goes
  and how it is signed." (Does init fill `publish.sign`? If not, say what to add.)
- "`pacto init --delivery --plan` / `pacto init --delivery`" and the three
  bullets it adds: a Pacto resource, one Stage for each environment ("such as
  test and prod": how are environments chosen?), the Flux OCIRepository and
  Kustomization.
- "Review the files and apply them with `kubectl apply`." (Does init write files,
  and where, or apply them itself?)
- "`pacto publish --propose test/my-service`" (the Stage name and namespace init
  generates) and "A Stage set to promote on its own (`auto: true`) needs no
  `--propose`" (does init set `auto` on the test Stage?).
- `pacto validate` right after `pacto init`: confirm the generated contract
  validates.

Also re-check `docs/cli-reference.md` `## pacto init` after `make docs-generate`
(generated, not edited here) and `docs/quickstart.md` step 2, which still
describes `pacto init <name>` in an empty directory (correct today and per the
ledger, but confirm the wording once init changes).

Other cross-track facts written from the ledger:

- `docs/publishing.md`: "attaches an SBOM made by `syft`", "and `syft` for
  SBOMs", and `docs/contract-reference/publish.md` `artifacts[].sbom` "made by
  `syft`". Ledger A removes syft (the build makes the SBOM, Pacto attaches and
  signs it). Rewrite both once core lands.
- `docs/publishing.md` signing table `kms:` row and
  `integrations/kubernetes/docs/delivery-records.md` "In production, sign with
  a KMS key": ledger A makes KMS a build-tag option. Name the build to use and
  which recorder image ships KMS.
- `docs/publishing.md` "there is no source revision (run it in a git checkout,
  or pass `--revision`)": confirm after the stdlib `.git` reader replaces
  `git rev-parse` (worktrees and packed refs).

## 3. Sentences to re-check once the dashboard work lands (dash track)

- `integrations/kubernetes/docs/delivery.md`, "Where you follow it": "an inbox
  of the changes waiting for your team, every environment and what it runs,
  the history of past changes and the published versions." Add blame and undo
  in History and recent refused publishes (ledger C) once they exist.
- `integrations/kubernetes/docs/delivery-identity.md`, "Follow delivery in the
  dashboard": the four views and the change page. Same additions; check the
  view names after the D2 header and navigation changes.
- `integrations/kubernetes/docs/delivery-limitations.md`, "Dashboard": "Published
  versions are the ones your Stages know about. A version no Stage has seen yet
  is not listed." Check once refused publishes show up.
- `integrations/kubernetes/docs/delivery-setup.md`: the dashboard's
  **Update these first** label.

## 4. Sentences to re-check once the logic work lands (logic track)

- `integrations/kubernetes/docs/delivery.md`: "Once checkout has moved, only
  the orders team is still asked." This is ledger C (story beat B6 on
  Kubernetes), not proven yet.
- `integrations/kubernetes/docs/delivery-setup.md`: "While a change waits, the
  promoter plans the order again, so a consumer that moved drops out." Confirm
  that the approval owners shrink too, not only the order.

## 5. Still open for ledger E

The required gate ("a newcomer reviewer understands the two front pages
without reading anything else") needs an independent reviewer; it is not
self-certified here.
