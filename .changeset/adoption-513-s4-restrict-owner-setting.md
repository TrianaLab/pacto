---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Only a member of a service's current owner or a default approver may set,
change or remove its service.owner through a declaration, on pacto declare,
pacto_declare and (once it can declare) the dashboard; agents and CI signers
never do, and on_behalf_of grants nothing. A refused owner change stops before
anything is pushed with OWNER_SIGNER_NOT_PERSON, OWNER_SIGNER_NOT_ALLOWED,
OWNER_POLICY_UNVERIFIED or OWNER_SIGNER_UNVERIFIED, and an agent's plan lists
who may accept it in mayAccept. An owner read from CODEOWNERS or
catalog-info.yaml is attested when a CI identity the publisher rule accepts
signed the bundle at the commit it was read at: it routes reviews and never
clears a gate. The fleet adds ownerSource on services, source on ownership details,
signerRepository and signerCommit on bundle signatures and ownerRight on
revision provenance, and ignores a refused or unchecked owner with the
limitations OWNER_DECLARATION_REFUSED and OWNER_DECLARATION_UNVERIFIED.
pacto fleet get and the TUI print where an attested owner was read. The
operator chart gains the declare values, rendered into an immutable,
content-named ConfigMap the operator mounts, passes to its dashboard and
publishes in its signed target snapshot when registry is set; the operator
sets the OwnerVerified condition on a running bundle that changes the owner
and withholds that owner from status unless its signer is allowed.
