---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Release CLI binaries, `checksums.txt` and the dashboard, operator and delivery
images now carry signed SLSA build-provenance attestations, verifiable with
`gh attestation verify`. The images no longer carry BuildKit's unsigned
per-platform provenance manifests.
