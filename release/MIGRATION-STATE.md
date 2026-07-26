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
