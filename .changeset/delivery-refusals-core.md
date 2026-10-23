---
"@pacto/core": minor
---

A publish the version rule refuses is kept as a signed refusal in the contract
bundle repository, signed with the publish key, and `pacto publish` says where
(or why it could not be kept, which never changes the outcome). The
dashboard's Published view reads these refusals for every service with a
Stage and shows the version, the bump it needed and the version it was
compared with; it does not check their signatures and says so. `pacto
validate` now also refuses an `sbom` on a `dockerfile` build, which writes no
SBOM file.
