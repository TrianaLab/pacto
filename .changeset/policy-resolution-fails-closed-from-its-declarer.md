---
"@pacto/core": minor
---

Resolve every policy reference from the contract that declared it, and fail closed
when the referenced schema cannot be read.

**Relative references now resolve against their declarer, not the working
directory.** A `ref:` written in a bundle three hops down the dependency chain was
resolved from wherever `pacto` happened to be invoked, so the same contract
validated differently depending on which directory you ran the command in. Worse,
a contract pulled from a registry could name a local directory and have Pacto read
a policy schema out of the invoking machine's filesystem. Resolution now carries
the declaring origin: a reference reached from a registry bundle can only ever
resolve to another registry bundle, and a local reference resolves relative to the
contract file that wrote it.

`validation.OriginBundleResolver` is the new port — `RootBase()` plus
`ResolveBundleFrom(base, ref)` — mirroring `graph.OriginContractFetcher`. The
widening is additive: `ResolveBundleFrom` is discovered at runtime, so
`ValidateWithResolver` and `ResolvePoliciesWithResolver` keep their signatures and
an existing `BundleResolver` implementation behaves exactly as it did. Cycle
detection keys on the pair (declaring base, reference text) rather than the text
alone, so a diamond in the reference graph is no longer misreported as a cycle.

**A referenced bundle whose policy schema cannot be read is now an error.** Three
conditions — an unreadable schema file, one that is not JSON and one that does not
compile — returned `nil` and dropped the policy silently whenever the referenced
bundle declared `policies[]`, while the sibling branch raised
`POLICY_REF_UNRESOLVED` on exactly the same three. A platform bundle shipped
without its declared schema therefore made every consumer's `pacto validate` and
`pacto push` pass with zero policies enforced. All three now raise
`POLICY_REF_UNRESOLVED`.

**A contract that used to pass may now fail.** That is the point: it was passing
because nothing was being enforced. If `pacto validate` starts reporting
`POLICY_REF_UNRESOLVED` against a bundle that was green before, the referenced
bundle is not shipping the schema its own `policies[]` block promises.

**`service.version` must be a single safe path component.** `pacto pack`
interpolates it straight into its output filename, so a version carrying a path
separator or naming the parent directory let a contract authored in a pull request
write its archive outside the build root. The pattern lands in the JSON Schema
rather than in the pack command because every consumer runs the schema — a guard
in `pack` alone would leave the operator, the dashboard and the MCP server open.
Every shape semver produces still passes, prerelease and build metadata included,
and so does a plain label like `latest`.
