#!/usr/bin/env node
// apply-release-plan.mjs — consume release/release-plan.json and MUTATE the working
// tree into release state, idempotently, then validate every mutated field.
//
// Every edit is keyed to a GENERIC pattern (a version regex, not the old value),
// so re-running produces a byte-identical tree: run twice -> the second `git diff`
// is empty. It also emits release/release-manifest.json (every release unit ->
// {version, coordinate, tag, artifactKind}).
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const R = (...p) => join(root, ...p);
const plan = JSON.parse(readFileSync(R('release', 'release-plan.json'), 'utf8'));

// ---- values from the plan (single source of truth) ----
const core = plan.groups.core;
const k8s = plan.groups.kubernetes;
const chart = k8s.artifacts.find((a) => a.kind === 'helm-chart');
const opImage = k8s.artifacts.find((a) => a.unit === 'operator-image');
const chartVersion = chart.chartVersion;      // e.g. 4.7.0
const appVersion = chart.appVersion;          // e.g. 4.7.0
const imageTag = chart.defaultImageTag;       // e.g. 4.7.0
const opImageCoord = opImage.coordinate;      // ghcr.io/.../pacto-controller
const compat = k8s.compatibility.pactoCore;   // e.g. >=2.0.0
const pin = k8s.goModPin;                      // { module, version:"v2.7.0" }

// Matches a semver core, optionally with a prerelease suffix.
const SEMVER = String.raw`\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?`;
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const changed = [];
function edit(relPath, fn) {
  const p = R(relPath);
  const before = readFileSync(p, 'utf8');
  const after = fn(before);
  if (after !== before) { writeFileSync(p, after); changed.push(relPath); }
  return after;
}
function assert(cond, msg) {
  if (!cond) { console.error(`apply-release-plan: FAIL — ${msg}`); process.exit(1); }
}

// ---- 1. integration go.mod: pin published core, assert NO replace remains ----
{
  const rel = 'integrations/kubernetes/go.mod';
  const after = edit(rel, (s) =>
    s.replace(new RegExp(`${esc(pin.module)} v${SEMVER}`), `${pin.module} ${pin.version}`));
  assert(after.includes(`${pin.module} ${pin.version}`),
    `${rel}: core require not pinned to ${pin.version}`);
  assert(!/^\s*replace(\s|\()/m.test(after),
    `${rel}: a replace directive is present — release state must have none`);
}

// ---- 2. operator chart Chart.yaml: version, appVersion, artifacthub image ----
{
  const rel = 'integrations/kubernetes/charts/pacto-operator/Chart.yaml';
  const after = edit(rel, (s) => {
    s = s.replace(/^version:.*$/m, `version: ${chartVersion}`);
    s = s.replace(/^appVersion:.*$/m, `appVersion: "${appVersion}"`);
    // Artifact Hub image annotation: add once, then keep its tag in sync.
    if (!s.includes('artifacthub.io/images')) {
      if (!s.endsWith('\n')) s += '\n';
      s += `annotations:\n  artifacthub.io/images: |\n    - name: pacto-controller\n` +
           `      image: ${opImageCoord}:${appVersion}\n`;
    }
    s = s.replace(new RegExp(`(${esc(opImageCoord)}:)v?${SEMVER}`), `$1${appVersion}`);
    return s;
  });
  assert(new RegExp(`^version: ${esc(chartVersion)}$`, 'm').test(after), `${rel}: version != ${chartVersion}`);
  assert(new RegExp(`^appVersion: "${esc(appVersion)}"$`, 'm').test(after), `${rel}: appVersion != ${appVersion}`);
  assert(after.includes(`${opImageCoord}:${appVersion}`), `${rel}: artifacthub image tag != ${appVersion}`);
}
  // image.tag intentionally NOT pinned in values.yaml: the chart deployment defaults
  // the tag to .Chart.AppVersion, so pinning it here would only create helm-docs drift.

// ---- 4. artifacthub-repo.yml consistency (repo metadata carries no version) ----
{
  const rel = 'integrations/kubernetes/artifacthub-repo.yml';
  assert(existsSync(R(rel)) && /repositoryID:/.test(readFileSync(R(rel), 'utf8')),
    `${rel}: missing or lacks repositoryID`);
}

// ---- 5. integration.yaml compatibility (pactoCore) ----
{
  const rel = 'integrations/kubernetes/integration.yaml';
  const after = edit(rel, (s) => s.replace(/(pactoCore:\s*)"[^"]*"/, `$1"${compat}"`));
  assert(after.includes(`pactoCore: "${compat}"`), `${rel}: pactoCore != ${compat}`);
}

// ---- 6. generated install examples: chart README badges + --version pin ----
{
  const rel = 'integrations/kubernetes/charts/pacto-operator/README.md';
  const after = edit(rel, (s) => {
    s = s.replace(new RegExp(`(--version )v?${SEMVER}`, 'g'), `$1${chartVersion}`);
    // helm-docs badges: alt text + shields.io URL, chart version and appVersion.
    s = s.replace(new RegExp(`(?<!App)Version: ${SEMVER}`, 'g'), `Version: ${chartVersion}`);
    s = s.replace(new RegExp(`(?<!App)Version-${SEMVER}-informational`, 'g'), `Version-${chartVersion}-informational`);
    s = s.replace(new RegExp(`AppVersion: ${SEMVER}`, 'g'), `AppVersion: ${appVersion}`);
    s = s.replace(new RegExp(`AppVersion-${SEMVER}-informational`, 'g'), `AppVersion-${appVersion}-informational`);
    return s;
  });
  assert(after.includes(`--version ${chartVersion}`), `${rel}: install snippet not pinned to ${chartVersion}`);
}

// ---- 7. release-manifest.json: every unit -> {version, coordinate, tag, artifactKind} ----
function stable(v) {
  if (Array.isArray(v)) return v.map(stable);
  if (v && typeof v === 'object') return Object.fromEntries(Object.keys(v).sort().map((k) => [k, stable(v[k])]));
  return v;
}
{
  const unitsDir = R('release', 'units');
  const unitCoord = {};
  for (const id of readdirSync(unitsDir)) {
    const pj = JSON.parse(readFileSync(join(unitsDir, id, 'package.json'), 'utf8'));
    unitCoord[pj.pacto.releaseUnit] = pj.pacto.coordinate;
  }
  const units = {};
  for (const g of Object.values(plan.groups)) {
    for (const a of g.artifacts) {
      const tag = a.tag ?? a.release ?? a.chartVersion ?? (a.version != null ? String(a.version) : String(g.version));
      units[a.unit] = {
        version: g.version,
        coordinate: a.coordinate ?? unitCoord[a.unit] ?? null,
        tag,
        artifactKind: a.kind,
      };
    }
  }
  const manifest = stable({ schema: 'pacto-release-manifest/v1', units });
  const rel = 'release/release-manifest.json';
  const text = JSON.stringify(manifest, null, 2) + '\n';
  const p = R(rel);
  if (!existsSync(p) || readFileSync(p, 'utf8') !== text) { writeFileSync(p, text); changed.push(rel); }
}

console.log(
  `apply-release-plan: core v${core.version}, kubernetes v${k8s.version} — ` +
  (changed.length ? `updated ${changed.length} file(s):\n  ${changed.join('\n  ')}` : 'no changes (already at release state)'));
