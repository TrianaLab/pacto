// Smoke test: load the built app.wasm in Node and exercise the in-wasm API the
// same way boot.js does in the browser. Verifies the engine end-to-end without
// a browser. Run after `make build` (or via `make smoke`).
import { readFile } from "node:fs/promises";

// Node provides globalThis.crypto (used by the Go runtime) already.
const wasmExec = await readFile("dist/wasm_exec.js", "utf8");
new Function(wasmExec)(); // defines globalThis.Go

const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(await readFile("dist/app.wasm"), go.importObject);

let ready;
const readyP = new Promise((r) => (ready = r));
globalThis.__pactoOnReady = () => ready();
go.run(instance); // parks on select{}; do not await
await readyP;

const call = (method, path, body = null) => globalThis.__pactoServe(method, path, body);
const json = (res) => JSON.parse(res.body);

// Compare dotted numeric versions so "1.9.0" sorts before "1.10.0", and return 0
// on equality so ties are consistent. Fixture versions are plain semver-shaped
// strings, so a non-numeric component just counts as 0 instead of throwing.
const byVersion = (a, b) => {
  const x = String(a.version ?? "").split(".");
  const y = String(b.version ?? "").split(".");
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const d = (parseInt(x[i], 10) || 0) - (parseInt(y[i], 10) || 0);
    if (d !== 0) return d;
  }
  return 0;
};

let failures = 0;
const check = (name, cond, detail) => {
  console.log(`${cond ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
  if (!cond) failures++;
};

check("GET /health 200", call("GET", "/health").status === 200);

// Source health is served off the fleet snapshot, so assert the CONTRACT the UI
// reads rather than the exact source list a detector happens to produce. An empty
// list is a FAILURE, not a pass: the nav renders one pill per source, and every
// pill prints "type: reason", so a blank reason is a dangling colon on screen.
const srcs = json(call("GET", "/api/sources"));
const srcList = Array.isArray(srcs.sources) ? srcs.sources : [];
check("GET /api/sources keeps its shape",
  srcList.length >= 1 && typeof srcs.discovering === "boolean"
    && srcList.every((s) => typeof s.type === "string" && s.type !== ""
      && typeof s.enabled === "boolean"
      && typeof s.reason === "string" && s.reason !== ""),
  `${srcList.length} sources`);

// ── Operational graph (fleet) + impact + capabilities ──
// The demo wires SetFleetProvider/SetImpactProvider/SetObservedAvailable, so
// every new endpoint the redesigned dashboard calls must answer for real.
const caps = json(call("GET", "/api/capabilities"));
check("capabilities: fleet+impact+observed enabled", caps.fleet === true && caps.impact === true && caps.observed === true, JSON.stringify(caps));

const snap = json(call("GET", "/api/fleet/snapshot"));
const svcCount = Object.keys(snap.services || {}).length;
check("fleet snapshot has all demo services", svcCount >= 7, `${svcCount} services`);
check("fleet snapshot has a snapshotId", typeof snap.snapshotId === "string" && snap.snapshotId.length > 0);
check("fleet snapshot has operational targets", Object.keys(snap.targets || {}).length >= 7);
// A deliberately-unavailable secondary source makes the snapshot partial.
check("fleet snapshot is partial (an unavailable source is surfaced)",
  snap.completeness !== "complete" && (snap.sources || []).some((s) => s.status === "unavailable"),
  `completeness=${snap.completeness}`);

const search = json(call("GET", "/api/fleet/services?text=payments"));
check("fleet search finds payments-service", (search.services || []).some((s) => s.name === "payments-service"));
const hit = (search.services || []).find((s) => s.name === "payments-service");
check("fleet search hit carries a domain-qualified key", hit && typeof hit.key === "string" && hit.key.length > 0, hit && hit.key);

const detail = json(call("GET", "/api/fleet/service?key=payments-service"));
check("fleet service detail has revisions + targets", detail.service && (detail.revisions || []).length >= 1 && (detail.targets || []).length >= 1);

const fgraph = json(call("GET", "/api/fleet/services/payments-service/graph?direction=dependents&transitive=true"));
check("fleet graph traverses from payments-service", fgraph.root === "payments-service");

const fstatus = json(call("GET", "/api/fleet/status"));
check("fleet status surfaces the non-compliant orders target",
  (fstatus.items || []).some((i) => i.name.includes("orders-service") || i.code === "NON_COMPLIANT"));

// A pick a target detail key from the snapshot and fetch it.
const someTargetKey = Object.keys(snap.targets || {})[0];
const tdetail = json(call("GET", `/api/fleet/target?key=${encodeURIComponent(someTargetKey)}`));
check("fleet target detail resolves by key", tdetail.target && tdetail.target.key === someTargetKey, someTargetKey);

// Impact: payments-service's oldest revision against its newest, resolved
// entirely from embedded bundles, no OCI. Uses the same published snapshot the
// graph serves. The pair is named from what was actually diffed -- the label
// used to read "1.0.0 → 2.0.0" while the code took the newest revision, so it
// had been reporting a comparison it never ran.
const payRevs = (detail.revisions || []).slice().sort(byVersion);
// Guarded so a detail response without revisions fails as this check rather than
// as a TypeError that takes the rest of the run down with it.
check("payments-service detail carries revisions to compare", payRevs.length >= 2, `${payRevs.length}`);
const oldRev = payRevs[0] || {};
const newRev = payRevs[payRevs.length - 1] || {};
const impRes = call("GET", `/api/fleet/impact?old=${encodeURIComponent(oldRev.resolvedRef)}&new=${encodeURIComponent(newRev.resolvedRef)}&includeObserved=false`);
check("impact 200", impRes.status === 200, `status ${impRes.status}`);
const imp = json(impRes);
check("impact result binds the published snapshot (section 2.2)", imp.snapshotId === snap.snapshotId, `${imp.snapshotId} vs ${snap.snapshotId}`);
check(`impact ${oldRev.version}→${newRev.version} is BREAKING`, imp.classification === "BREAKING", imp.classification);
check("impact has affected consumers (direct + transitive)", (imp.consumers || []).length >= 1);

// Observed: include-observed surfaces the audit-log shadow consumer that declares
// no dependency on payments-service.
const impObs = json(call("GET", `/api/fleet/impact?old=${encodeURIComponent(oldRev.resolvedRef)}&new=${encodeURIComponent(newRev.resolvedRef)}&includeObserved=true`));
check("include-observed surfaces the audit-log shadow consumer",
  (impObs.consumers || []).some((c) => c.service === "audit-log"),
  (impObs.consumers || []).map((c) => c.service).join(","));

// Readiness showcase, read off the newest revision in the fleet service detail --
// the same revision the service page lands on. payments-service 2.1.1 declares a
// readiness block that fails its gate (a stale ai-evals check drops the score to
// 70 < 80); orders-service 1.2.0 declares an all-current block that passes. The
// revision embeds the raw readiness.Result untagged, hence the capitalised keys.
check("payments newest revision exposes readiness", newRev.readiness != null, newRev.version);
check("payments readiness Score 70", newRev.readiness && newRev.readiness.Score === 70, newRev.readiness && `score ${newRev.readiness.Score}`);
check("payments readiness gate FAIL", newRev.readiness && newRev.readiness.Passing === false);

const ordRevs = (json(call("GET", "/api/fleet/service?key=orders-service")).revisions || [])
  .slice().sort(byVersion);
const ordRev = ordRevs[ordRevs.length - 1] || {};
check("orders newest revision readiness passes", ordRev.readiness && ordRev.readiness.Passing === true,
  ordRev.readiness && `score ${ordRev.readiness.Score}`);

// ── Product API: the demo must be RICH through the paths the product UI uses ──
// The product UI never reads the raw snapshot above; it reads these endpoints. A
// demo that is rich in the snapshot and thin here shows an empty product, so the
// richness is asserted where the user actually sees it.
const ov = json(call("GET", "/api/fleet/overview"));
const ovs = ov.summary || {};
check("product overview reports the full estate", ovs.services >= 7 && ovs.targets >= 8, `${ovs.services} services / ${ovs.targets} targets`);
// Every posture bar needs more than one non-zero bucket or it renders as one block.
check("compliance is a real distribution", ovs.compliantTargets > 0 && ovs.nonCompliantTargets > 0 && ovs.unknownTargets > 0,
  `${ovs.compliantTargets}/${ovs.nonCompliantTargets}/${ovs.unknownTargets}`);
check("revision-match certainty is a real distribution", ovs.exactTargetLinks > 0 && ovs.inferredTargetLinks > 0 && ovs.unresolvedTargetLinks > 0,
  `exact=${ovs.exactTargetLinks} inferred=${ovs.inferredTargetLinks} unresolved=${ovs.unresolvedTargetLinks}`);
check("evidence freshness partitions the population (fresh / stale / never observed)",
  ovs.evidence && ovs.evidence.withEvidence + ovs.evidence.withoutEvidence === ovs.targets
    && ovs.evidence.stale > 0 && ovs.evidence.withoutEvidence > 0,
  ovs.evidence && `${ovs.evidence.withEvidence}+${ovs.evidence.withoutEvidence} of ${ovs.targets}, ${ovs.evidence.stale} stale`);
check("source health shows available, partial and unavailable",
  ovs.degradedSources > 0 && ovs.unavailableSources > 0, `degraded=${ovs.degradedSources} unavailable=${ovs.unavailableSources}`);

const entity = (kind, key) => json(call("GET", `/api/fleet/entities/${kind}?key=${encodeURIComponent(key)}`));
const svcKey = hit.key;
const psvc = entity("service", svcKey).service || {};
check("service detail runs in more than one scope", (psvc.deployments || {}).total >= 2, `${(psvc.deployments || {}).total} targets`);
check("service summary carries every posture dimension",
  psvc.summary && psvc.summary.compliance && psvc.summary.links && psvc.summary.evidence);

// The target page is the runtime inspector: labels and observed runtime are the
// two things only a collector can supply, and the page is empty without them.
const prodTarget = (psvc.deployments.items || []).find((t) => t.key.startsWith("prod/"));
const ptgt = entity("target", prodTarget.key).target || {};
check("target detail carries observed runtime", (ptgt.observedRuntime || {}).total >= 2, `${(ptgt.observedRuntime || {}).total} values`);
check("target detail carries labels", (ptgt.labels || {}).total >= 2, `${(ptgt.labels || {}).total} labels`);
check("target is corroborated by two collectors", (ptgt.sources || {}).total >= 2, ((ptgt.sources || {}).items || []).join(","));
check("target identity is an EXACT digest match", ptgt.identity && ptgt.identity.identityClass === "exact", ptgt.identity && ptgt.identity.identityClass);

// The owner of a service that is actually running: an owner with nothing deployed
// legitimately has an empty posture, and asserting against one would prove nothing.
const own = entity("owner", psvc.ownership.ref.key).owner || {};
check("owner detail carries a complete-population summary",
  own.summary && own.summary.services >= 1 && own.summary.targets >= 1,
  own.summary && `${own.summary.services} services / ${own.summary.targets} targets`);

const attn = json(call("GET", "/api/fleet/attention"));
const sevs = new Set((attn.items || []).map((i) => i.severity));
check("attention spans more than one severity", sevs.size >= 2, [...sevs].join(","));

console.log(failures === 0 ? "\nALL CHECKS PASSED" : `\n${failures} CHECK(S) FAILED`);
process.exit(failures === 0 ? 0 : 1);
