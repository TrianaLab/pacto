#!/usr/bin/env node
// Deterministic release-plan generator. Reads the Changesets-managed release-unit
// versions (release/units/*/package.json) + integration manifests and emits a
// machine-readable release plan: Go module tags, image tags, chart version/
// appVersion, compatibility, the release-state go.mod pin (no replace), and the
// deterministic publish ORDER that keeps every published module resolvable.
//
// Pure + deterministic: running it twice produces a byte-identical plan.
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { createHash } from 'node:crypto';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const unitsDir = join(root, 'release', 'units');

function readUnits() {
  const out = {};
  for (const id of readdirSync(unitsDir).sort()) {
    const pj = JSON.parse(readFileSync(join(unitsDir, id, 'package.json'), 'utf8'));
    out[pj.pacto.releaseUnit] = { version: pj.version, kind: pj.pacto.kind, coordinate: pj.pacto.coordinate };
  }
  return out;
}

function buildPlan(u) {
  const core = u['core'].version;             // e.g. 2.7.0
  const k8s = u['k8s-module'].version;        // e.g. 4.7.0
  // The core Go module path carries its major (github.com/trianalab/pacto/vN).
  // The published core version must therefore be on that major. Until the first
  // vN release is published, the unit version still reads the last-published
  // vN-1 (changesets bumps it to vN.0.0 at release time), so derive the pin +
  // compatibility from the PATH major, not the un-bumped unit version — this
  // keeps the go.work dev build + apply-release-plan on a valid vN and lands
  // exactly on vN.0.0 when the major changeset applies.
  const pathMajor = Number((u['core'].coordinate.match(/\/v(\d+)$/) || [, core.split('.')[0]])[1]);
  const coreMajor = Number(core.split('.')[0]);
  const pinVersion = pathMajor > coreMajor ? `v${pathMajor}.0.0` : `v${core}`;
  const compatMajor = Math.max(pathMajor, coreMajor);
  // Fixed groups: core line and k8s line move as a unit.
  const plan = {
    schema: 'pacto-release-plan/v1',
    groups: {
      core: {
        version: core,
        tags: [`v${core}`],                                   // root module tag
        artifacts: [
          { unit: 'core', kind: 'go-module', coordinate: u['core'].coordinate, tag: `v${core}` },
          { unit: 'cli', kind: 'binaries', release: `v${core}` },
          { unit: 'dashboard-image', kind: 'oci-image', coordinate: u['dashboard-image'].coordinate, tag: core },
          // Dashboard CONTRACT BUNDLE (pactos/pacto-dashboard) — the OCI-distributed
          // pacto contract for the dashboard service, published by .github/workflows/
          // pacto.yml. Distinct coordinate from the dashboard IMAGE above; owned here
          // so the one-publisher gate covers it and its version tracks the core line.
          { unit: 'dashboard-contract-bundle', kind: 'oci-image', coordinate: u['dashboard-contract-bundle'].coordinate, tag: core },
          // Demo OCI contract bundles (examples/demo/bundles) publish as a single
          // owned namespace. The per-bundle tags are the contract versions; this
          // unit tag tracks the core line so ownership + version move together. The
          // maintainer publishes to the production coordinate; the PR proof pushes
          // to a staging registry (release/scripts/publish-demo-bundles.sh).
          { unit: 'demo-bundles', kind: 'oci-image', coordinate: u['demo-bundles'].coordinate, tag: core },
        ],
      },
      kubernetes: {
        version: k8s,
        tags: [`integrations/kubernetes/v${k8s}`],            // nested-module tag
        // Release state: the integration go.mod pins the published core, NO replace.
        goModPin: { module: u['core'].coordinate, version: pinVersion },
        // Fail-closed: apply-release-plan asserts no replace directive survives into
        // release state (it never strips one — a stray replace is a mistake to surface,
        // not silently rewrite). Dev builds resolve via go.work, so none should exist.
        assertNoReplace: true,
        artifacts: [
          { unit: 'k8s-module', kind: 'go-module', coordinate: u['k8s-module'].coordinate, tag: `integrations/kubernetes/v${k8s}` },
          { unit: 'operator-image', kind: 'oci-image', coordinate: u['operator-image'].coordinate, tag: k8s },
          { unit: 'operator-chart', kind: 'helm-chart', coordinate: u['operator-chart'].coordinate, chartVersion: k8s, appVersion: k8s, defaultImageTag: k8s },
          { unit: 'k8s-docs', kind: 'docs', version: k8s },
        ],
        compatibility: { pactoCore: `>=${compatMajor}.0.0` },
      },
    },
    // Deterministic publish order: core module FIRST (so the k8s module's pin
    // resolves), then k8s group. Every step leaves published modules resolvable.
    publishOrder: [
      'core:go-module', 'core:dashboard-image', 'core:dashboard-contract-bundle',
      'core:cli', 'core:demo-bundles',
      'kubernetes:go-mod-pin', 'kubernetes:go-module', 'kubernetes:operator-image',
      'kubernetes:operator-chart', 'kubernetes:docs',
    ],
  };
  return plan;
}

// Stable stringify (sorted keys) for byte-identical output.
function stable(v) {
  if (Array.isArray(v)) return v.map(stable);
  if (v && typeof v === 'object') {
    return Object.fromEntries(Object.keys(v).sort().map((k) => [k, stable(v[k])]));
  }
  return v;
}

// buildTransaction — the release-ready plan (release/DESIGN-release-safety.md).
// It is the ONLY source of release intent: publishing keys off `ready` +
// `changedUnits`, never off a file diff. `changedUnits` is every unit whose
// new version (from the just-bumped release/units) differs from the previous
// version recorded in the on-disk release-manifest.json — so a feature merge
// that bumps nothing yields an empty, not-ready transaction and publishes
// nothing. Fully deterministic: no clock, no randomness.
function readPrevVersions() {
  const p = join(root, 'release', 'release-manifest.json');
  if (!existsSync(p)) return {};
  const m = JSON.parse(readFileSync(p, 'utf8'));
  const out = {};
  for (const [unit, v] of Object.entries(m.units || {})) out[unit] = v.version;
  return out;
}

function buildTransaction(plan, units, prev) {
  const newVersions = {};
  const expectedTags = {};
  const expectedCoordinates = {};
  const unitGroup = {};
  for (const [groupName, g] of Object.entries(plan.groups)) {
    for (const a of g.artifacts) {
      newVersions[a.unit] = String(g.version);
      expectedTags[a.unit] = a.tag ?? a.release ?? a.chartVersion ??
        (a.version != null ? String(a.version) : String(g.version));
      expectedCoordinates[a.unit] = a.coordinate ?? units[a.unit]?.coordinate ?? null;
      unitGroup[a.unit] = groupName;
    }
  }
  // A unit is changed iff its version moved (or it is brand new).
  const changedUnits = Object.keys(newVersions)
    .filter((u) => prev[u] !== newVersions[u])
    .sort();
  const changedGroups = [...new Set(changedUnits.map((u) => unitGroup[u]))].sort();
  const previousVersions = {};
  for (const u of Object.keys(newVersions)) previousVersions[u] = prev[u] ?? null;

  const sha16 = (obj) => createHash('sha256')
    .update(JSON.stringify(stable(obj))).digest('hex').slice(0, 16);
  const manifestSha = createHash('sha256')
    .update(JSON.stringify(stable(newVersions))).digest('hex');
  const transactionId = changedUnits.length
    ? sha16({ changedUnits, newVersions, previousVersions }) : '';

  const units_ = {};
  for (const u of changedUnits) units_[u] = { status: 'pending' };

  return stable({
    schema: 'pacto-release-transaction/v1',
    ready: changedUnits.length > 0,
    transactionId,
    sourceSha: '',                 // filled at release time = GITHUB_SHA
    manifestSha,
    changedGroups,
    changedUnits,
    previousVersions,
    newVersions,
    expectedTags,
    expectedCoordinates,
    dependencyOrder: plan.publishOrder,
    units: units_,
  });
}

const units = readUnits();
const plan = buildPlan(units);
const outPath = join(root, 'release', 'release-plan.json');
writeFileSync(outPath, JSON.stringify(stable(plan), null, 2) + '\n');
console.log(`wrote ${outPath} (core v${plan.groups.core.version}, kubernetes v${plan.groups.kubernetes.version})`);

// The transaction is written only with --transaction (the release:version path),
// so standalone plan / artifact-drift / dry-run runs never clobber it.
if (process.argv.includes('--transaction')) {
  const txn = buildTransaction(plan, units, readPrevVersions());
  const txnPath = join(root, 'release', 'release-transaction.json');
  writeFileSync(txnPath, JSON.stringify(txn, null, 2) + '\n');
  console.log(`wrote ${txnPath} (ready=${txn.ready}, changedUnits=[${txn.changedUnits.join(',')}])`);
}
