# Monorepo migration — state ledger (WIP, single PR)

## M1 DONE (proven)
- Import: operator @199de04 subtree-imported into integrations/kubernetes (history preserved; 199de04 is an ancestor; 694 commits).
- Module: github.com/trianalab/pacto-operator -> github.com/trianalab/pacto/integrations/kubernetes; replace ../pacto DROPPED; root go.work.
- Build/tests: engine ci-test 100% + operator ci-test 100% via go.work (NO replace). vet clean.
- External-consumer proof: temp module OUTSIDE workspace, GOWORK=off, no replace, `go get pacto/v2@v2.8.0-mono` (staging tag, local) -> build+run OK importing pkg/evidence+pkg/finding. Core is standalone-consumable.

## M2 (gate DONE; collector/operator physical split PENDING)
- tests/architecture/boundary_test.go: core (contract/evidence/finding/validation) has ZERO k8s/integration deps. PASS.

## M3 (design DONE; from inventory workflow — artifact-pipeline-ledger.json, 36 rows)
KEY FINDINGS:
- DUPLICATE PUBLISHER: CLI binaries+checksums published by BOTH engine auto-release.yml AND release.yml (latent double-publish; release.yml dormant only because token-created releases don't fire the release event). -> collapse to ONE publisher (M5).
- TAG-NAMESPACE COLLISION (biggest blocker): engine auto-release cuts bare vX.Y.Z (v2.7.0) AND operator auto-release cuts bare vX.Y.Z (v4.7.0) -> collide in monorepo. Must use prefixed tags: core vX.Y.Z (root), integration integrations/kubernetes/vA.B.C (M4).
- HISTORICAL VERSIONS: engine module v0.0.1..v2.7.0; OPERATOR image/chart at v4.7.0 (NOT v2.x — preserve v4.x continuity, do NOT reset). dashboard image + demo OCI separate.
- DEMO OCI (15 pkgs ghcr.io/trianalab/pacto-demo/*): UNOWNED in monorepo (was manual `pacto-demo make push`); consumed by config/samples + embedded offline in examples/demo WASM. -> assign owner (M7).
- go list -m pacto version BREAKS under same-repo workspace (DASHBOARD_IMAGE empties in engine pacto.yml) -> fix version source (M5).
- SIGNING: operator image+chart cosign-signed; engine CLI has NO SBOM/provenance/signing -> add (M12/M5).

## M4 blocker-elimination PROVEN (release-ordering, section 8) — both directions
- CORE standalone-consumable: ext module, GOWORK=off, no replace, `go get pacto/v2@v2.8.0-mono` -> build+run OK (imports pkg/evidence+pkg/finding). (release/proofs above)
- OPERATOR module standalone-buildable: integration tree copied OUTSIDE workspace (no go.work), go.mod require bumped to staged core v2.8.0-mono, replace=0 -> `go mod download` + `go build ./...` + controller binary (77MB) built OK; collector+controller compile against staged core. (release/proofs/operator-standalone-build.txt)
=> The exact section-8 bar met: no local replace, resolves from a clean external module, builds outside the monorepo. Blocker structurally eliminated (not hidden): go.work for dev/CI; staged-version pin for release state; tested tag/publish order to be automated in M4/M5 Changesets layer.
NOTE: v2.8.0-mono is a LOCAL staging tag for the proof only (not pushed).

## REMAINING (M4 finish -> M9): Changesets multi-target version/publish layer (release:version + release:publish, staging only), consolidated CI + one-publisher-per-artifact release pipeline (collapse CLI double-publisher; prefixed tags; fix go-list version source), generated MkDocs integration docs + mike versioning, demos (offline scope-labeled + real kind journey) + demo-artifact republish sim, exhaustive OCI cache/order/version matrix + real kind e2e + fuzz/race, staging release DRY-RUN, doc-assertion re-audit, independent final review, 30-item report. No prod publish; no merge.

## M9 review fixes (2026-07-26)
- B2 (release-order reproducibility) RESOLVED: committed SOURCE pins last-published core v2.7.0 (dev builds via go.work local source — go.work uses local source which HAS pkg/evidence+finding even though published v2.7.0 lacks them). PENDING changesets (.changeset/core-adds-evidence-finding.md minor + k8s-repin-published-core.md patch) declare the next release. release/scripts/verify-standalone.sh REPRODUCIBLY proves the release state: stages core@next (v2.8.0, computed from changesets) via a process-scoped git config + local staging tag, sets the operator go.mod to v2.8.0, GOWORK=off + replace=0 -> operator builds standalone. Wired into `make release-dry-run`. Committing v2.8.0 (unpublished) would BREAK the go.work dev build (go reads the required version's go.mod), so apply-release-plan's v2.8.0 pin is a RELEASE-TIME transform (run in CI after core is published), never committed to source.
- B1 (helm-docs drift) RESOLVED: values.yaml image.tag = "" (deployment defaults tag -> .Chart.AppVersion); apply-release-plan no longer pins the tag; helm-docs regenerated; helm-docs-check GREEN.
