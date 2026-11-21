---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

A dependency on a service of your own may leave out ref in a 2.5 contract:
Pacto resolves it by name in its domain. pacto ci proposes its ref from the
organization index and, from the target snapshot, a ^MAJOR.MINOR.0 range for a
dependency whose range is unknown. Both land in pacto.inferred.yaml and
publish as compatibility: unknown. pacto accept --proposed (or --field for one
field) signs, after the person's interactive Sigstore login, an acceptance
record attached to the service's newest published version: a referrer of
type application/vnd.pacto.acceptance.v1+json, or a write-once
pacto-accept-<subject>-<manifest> tag on a registry without the Referrers API.
Only a record signed by the service's declared owner counts; any other
signer, and every agent through the new MCP tool pacto_accept, writes a
pacto-accept-draft-<n> a person accepts with --draft. pacto ci --publish
carries counted ranges into the next version's pacto.yaml and cites each
record in inputs.acceptances. pacto accept --break <provider>@<version>
accepts a break announced in breaks[], also before the provider publishes
(--from); the gate clears with BREAKING_ACCEPTED once every consumer accepted,
and an announcement without expires expires 30 days after the commit time.
pacto ci, pacto impact, pacto_impact and Change analysis print a Ranges:
coverage line, and --policy unknown-range=warn|fail and
uncontracted=warn|fail:<window> hold a BREAKING change that no range covers.
A changed spec file that is only inferred prints a possible break (inferred)
advisory with the YAML that declares the interface: the row reads unverified
(INFERRED_POSSIBLE_BREAK) and the classification and version never change.
The dashboard service page shows the plan and the pacto accept command for
Accept all proposed ranges; it never signs or writes.
