---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Make a demo-fixture edit unable to half-ship a release.

Release run 32560058692 published four irreversible units and then died. Two
independent defects had to line up for that, and both are closed here.

The demo bundles publish to immutable tags. `payments-service` 2.1.0 was edited
in place — a mermaid diagram added to a version already published — so the
byte-exact gate correctly refused the tag, but it refused it mid-release,
because nothing ran that gate before the release. The fixture is restored to its
published bytes and the diagram ships as a new `payments-service` 2.1.1, and
`publish-demo-bundles.sh --check` now runs the identical gate read-only at PR
time as the `demo-bundle-immutability` CI leg.

Separately, the `demo-compose` job lost its ORAS install when the unit moved to
`docker compose publish`, on the reasoning that ORAS stayed where the ledger
used it — while that job still read and wrote the ledger, which *is* the ORAS
user. `ledger.sh` returned the empty string for a missing binary, the empty
string means "nothing recorded", and the unit failed closed. `ledger.sh` now
refuses to run without its tools and distinguishes a 404 from an unreadable
registry; the two `if [ "$(ledger.sh …)" ]` call sites that discarded its exit
status now assign first; and a new gate walks every job's shell through its make
targets and scripts and fails when a job can reach a CLI it never installed.
That gate found a second, quieter instance: the release dry run was rehearsing
without `syft`, silently skipping the SBOM the real release produces.
