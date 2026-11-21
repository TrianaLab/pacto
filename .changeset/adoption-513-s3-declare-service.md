---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

pacto declare publishes a contract for a workload the cluster already runs as
a contract-only version: 0.1.0 for a first declaration, else the published
baseline raised by the change, with the baseline's deploy artifact reused and
nothing built. Only a person's interactive Sigstore login (a browser, or a
device code with --device) publishes a declared version; a key, an ambient
OIDC token or a CI identity writes a write-once draft-<n> tag that a person
accepts with --from-draft. The bundle's pacto.inferred.yaml gains a
declaration block naming the signer, the inferred workload, the baseline and
each proposed range accepted with --accept. pacto pull gains --verify-identity,
--verify-issuer and --verify-key. The MCP tool pacto_declare is the first tool
of the agent write contract (the existing pacto_publish_plan and pacto_publish
stay outside it): it plans by default with an intent digest, always writes a
draft carrying on_behalf_of and refuses with a stable code and the command a
person runs. The fleet, explain, diff and the dashboard show a declaration's
cluster facts, coverage and signer only when the bundle carries a verified
signature by that person and the installation signer policy allows them.
