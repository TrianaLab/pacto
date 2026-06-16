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

let failures = 0;
const check = (name, cond, detail) => {
  console.log(`${cond ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
  if (!cond) failures++;
};

check("GET /health 200", call("GET", "/health").status === 200);

const services = json(call("GET", "/api/services"));
check("GET /api/services returns fleet", Array.isArray(services) && services.length >= 10, `${services.length} services`);
check("fleet includes payments-service@2.1.0",
  services.some((s) => s.name === "payments-service" && s.version === "2.1.0"));

const graphRes = call("GET", "/api/graph");
check("GET /api/graph 200 with content", graphRes.status === 200 && graphRes.body.length > 100, `${graphRes.body.length} bytes`);

const versions = json(call("GET", "/api/services/payments-service/versions"));
check("payments-service has 5 versions", Array.isArray(versions) && versions.length === 5, `${versions.length}`);
const v200 = versions.find((v) => v.version === "2.0.0");
check("2.0.0 classified BREAKING", v200 && v200.classification === "BREAKING", v200 && v200.classification);

const diff = json(call("GET", "/api/diff?from_name=payments-service&from_version=1.2.0&to_name=payments-service&to_version=2.0.0"));
check("diff 1.2.0->2.0.0 BREAKING", diff.classification === "BREAKING", `${diff.changes && diff.changes.length} changes`);
check("diff includes /charges removal", (diff.changes || []).some((c) => c.path === "openapi.paths[/charges]"));

// Every other GET endpoint the UI's api.ts calls must answer (no 500s), so no
// dashboard view errors out in the browser.
const n = encodeURIComponent("payments-service");
for (const [name, path] of [
  ["sources", "/api/sources"],
  ["service sources", `/api/services/${n}/sources`],
  ["dependents", `/api/services/${n}/dependents`],
  ["cross-refs", `/api/services/${n}/refs`],
  ["service graph", `/api/services/${n}/graph`],
]) {
  const res = call("GET", path);
  check(`GET ${path} ok`, res.status === 200, `${name} status ${res.status}`);
}

// Readiness showcase: payments-service 2.1.0 declares a readiness block that
// fails its gate (an expired ai-evals check drops the score to 70 < 80);
// orders-service 1.2.0 declares an all-current block that passes.
const pay = json(call("GET", "/api/services/payments-service"));
check("payments 2.1.0 exposes readiness", pay.readiness != null);
check("payments readiness Score 70", pay.readiness && pay.readiness.score === 70, pay.readiness && `score ${pay.readiness.score}`);
check("payments readiness gate FAIL", pay.readiness && pay.readiness.passing === false);

const ord = json(call("GET", "/api/services/orders-service"));
check("orders 1.2.0 readiness passes", ord.readiness && ord.readiness.passing === true, ord.readiness && `score ${ord.readiness.score}`);

console.log(failures === 0 ? "\nALL CHECKS PASSED" : `\n${failures} CHECK(S) FAILED`);
process.exit(failures === 0 ? 0 : 1);
