#!/usr/bin/env node
// release/scripts/publish.mjs — STAGING-ONLY idempotent, resumable, digest-aware
// release publisher.
//
// It pushes ONE synthetic OCI artifact per release unit (from
// release/release-manifest.json, in release-plan.json publishOrder) to a LOCAL
// disposable staging registry, proving the publish / idempotency / immutability
// / refusal semantics WITHOUT ever touching a production coordinate. The REAL
// production publish is done by .github/workflows/release.yml — never here.
//
// Idempotency is digest-aware at the LAYER (content) level: the layer digest is
// sha256 of the pushed bytes and is independent of oras' nondeterministic
// manifest annotations, so "already published" is an exact byte comparison.
//
// Every fail-closed refusal has a distinct error `code` (see PublishError).
import { readFileSync, writeFileSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const R = (...p) => join(root, ...p);
const LEDGER_PATH = R('release', '.release-ledger.json');
export const DEFAULT_STAGING = 'localhost:5001';
export const STATES = ['planned', 'built', 'verified', 'published', 'failed', 'resumable', 'complete'];

// ---- errors: each refusal is a distinct, catchable code ----
export class PublishError extends Error {
  constructor(code, message) { super(message); this.name = 'PublishError'; this.code = code; }
}
const refuse = (code, msg) => { throw new PublishError(code, msg); };

// ---- production guard ----------------------------------------------------
// Refuse any production-looking target (ghcr.io / the trianalab namespace)
// unless PACTO_ALLOW_PROD=1. Staging tooling must NEVER set that.
export function resolveTarget(env = process.env) {
  const target = env.PACTO_STAGING_REGISTRY || DEFAULT_STAGING;
  const looksProd = /ghcr\.io/i.test(target) || /trianalab/i.test(target.split('/').slice(1).join('/'));
  if (looksProd && env.PACTO_ALLOW_PROD !== '1') {
    refuse('production-target-refused',
      `refusing production-looking target "${target}" — point PACTO_STAGING_REGISTRY at a disposable local registry`);
  }
  return target;
}

// ---- coordinate -> staging ref ------------------------------------------
export function sanitizeTag(tag) {
  // OCI tag charset is [A-Za-z0-9_][A-Za-z0-9._-]{0,127}. Nested-module tags
  // like "integrations/kubernetes/v4.7.0" contain slashes and are not valid
  // tags, so collapse anything outside the charset to '-'.
  return String(tag).replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^[.-]+/, '').slice(0, 128);
}

export function repoPath(coordinate, kind) {
  // Map a release coordinate to a lowercase OCI repo path. Registry coordinates
  // keep their path (host stripped); non-registry coordinates (go module,
  // binaries, docs) get a namespaced synthetic path.
  const c = String(coordinate).replace(/^oci:\/\//, '');
  if (kind === 'oci-image' || kind === 'helm-chart') {
    const parts = c.split('/');
    if (parts[0].includes('.') || parts[0].includes(':')) parts.shift(); // drop host
    return parts.join('/').toLowerCase();
  }
  const ns = kind === 'go-module' ? 'gomod' : kind === 'binaries' ? 'releases' : 'docs';
  const path = c.replace(/[^A-Za-z0-9._/-]+/g, '/').replace(/\/+/g, '/').replace(/^\/|\/$/g, '');
  return `${ns}/${path}`.toLowerCase();
}

export function stagingRef(target, u) {
  return `${target}/${repoPath(u.coordinate, u.artifactKind)}:${sanitizeTag(u.tag)}`;
}

// ---- deterministic content + digest -------------------------------------
function canonical(v) {
  if (Array.isArray(v)) return v.map(canonical);
  if (v && typeof v === 'object') return Object.fromEntries(Object.keys(v).sort().map((k) => [k, canonical(v[k])]));
  return v;
}
export function unitContent(unit, u) {
  return JSON.stringify(canonical({ unit, kind: u.artifactKind, coordinate: u.coordinate, tag: u.tag, version: u.version })) + '\n';
}
export const sha256 = (buf) => 'sha256:' + createHash('sha256').update(buf).digest('hex');

// ---- registry ops (oras) ------------------------------------------------
const ARTIFACT_TYPE = 'application/vnd.pacto.release.unit.v1+json';
function orasFlags(target) {
  return /^(localhost|127\.0\.0\.1)(:|\/|$)/.test(target) ? ['--plain-http'] : [];
}
// Returns the content (layer) digest already published at ref, or null if absent.
export function remoteLayerDigest(ref, target) {
  try {
    const out = execFileSync('oras', ['manifest', 'fetch', ...orasFlags(target), ref],
      { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] });
    return JSON.parse(out).layers?.[0]?.digest ?? null;
  } catch { return null; }
}
export function pushArtifact(ref, target, content) {
  const dir = join(tmpdir(), `pacto-publish-${process.pid}-${Math.random().toString(36).slice(2)}`);
  mkdirSync(dir, { recursive: true });
  const file = join(dir, 'release-unit.json');
  writeFileSync(file, content);
  try {
    execFileSync('oras', ['push', ...orasFlags(target), '--disable-path-validation',
      '--artifact-type', ARTIFACT_TYPE, ref, `${file}:${ARTIFACT_TYPE}`],
      { stdio: ['ignore', 'ignore', 'pipe'] });
  } finally { rmSync(dir, { recursive: true, force: true }); }
}

// ---- release state ------------------------------------------------------
export function loadReleaseState(root_ = root) {
  const rd = (f) => JSON.parse(readFileSync(join(root_, 'release', f), 'utf8'));
  return { manifest: rd('release-manifest.json'), plan: rd('release-plan.json') };
}

// Resolve publishOrder -> ordered unit ids (skipping non-artifact steps such as
// the go-mod-pin source mutation). Each step is "<group>:<token>" where token is
// an artifact kind or a unit id.
export function resolveOrder(plan) {
  const out = [];
  for (const step of plan.publishOrder || []) {
    const [group, token] = step.split(':');
    if (token === 'go-mod-pin') continue; // source mutation, not a published artifact
    const arts = plan.groups?.[group]?.artifacts || [];
    const a = arts.find((x) => x.kind === token) || arts.find((x) => x.unit === token);
    out.push({ step, unit: a ? a.unit : null });
  }
  return out;
}

// ---- fail-closed refusals ----------------------------------------------
export function defaultGitStatus() {
  return execFileSync('git', ['-C', root, 'status', '--porcelain'], { encoding: 'utf8' });
}
// (f) release from a dirty tree / untrusted source.
export function checkCleanTree(env = process.env, gitStatus = defaultGitStatus) {
  // ponytail: staging-only escape hatch — release.yml publishes from a clean
  // merged checkout and never sets this; production safety is unaffected.
  if (env.PACTO_STAGING_ALLOW_DIRTY === '1') return;
  const dirty = gitStatus();
  if (dirty.trim() !== '') {
    refuse('dirty-tree', `release tree is not clean (git status --porcelain is non-empty) — commit or stash first:\n${dirty.trim()}`);
  }
}
// (b) two units must not claim the same published coordinate.
export function checkOwnership(manifest) {
  const seen = new Map();
  for (const [unit, u] of Object.entries(manifest.units)) {
    if (seen.has(u.coordinate)) {
      refuse('duplicate-artifact-ownership', `coordinate "${u.coordinate}" is claimed by both "${seen.get(u.coordinate)}" and "${unit}"`);
    }
    seen.set(u.coordinate, unit);
  }
}
// (c) every manifest unit needs a publisher; (d) every publisher needs a unit.
export function checkPublisherMapping(manifest, plan) {
  const order = resolveOrder(plan);
  const published = new Set(order.map((o) => o.unit).filter(Boolean));
  const units = new Set(Object.keys(manifest.units));
  for (const unit of units) {
    if (!published.has(unit)) refuse('unit-without-publisher', `release unit "${unit}" has no publisher (absent from release-plan publishOrder)`);
  }
  for (const o of order) {
    if (!o.unit) refuse('publisher-without-unit', `publishOrder step "${o.step}" does not resolve to any release unit`);
    if (!units.has(o.unit)) refuse('publisher-without-unit', `publishOrder step "${o.step}" -> unit "${o.unit}" is not in the release manifest`);
  }
  return order;
}
// (e) operator chart appVersion must equal the operator image version it deploys.
export function checkChartAppVersion(plan) {
  const k = plan.groups?.kubernetes;
  const chart = k?.artifacts.find((a) => a.kind === 'helm-chart');
  const img = k?.artifacts.find((a) => a.unit === 'operator-image');
  if (!chart || !img) return;
  const imgVer = String(img.tag ?? img.version);
  if (String(chart.appVersion) !== imgVer) {
    refuse('chart-appversion-mismatch', `operator chart appVersion "${chart.appVersion}" != operator image version "${imgVer}"`);
  }
  if (String(chart.chartVersion) !== String(k.version)) {
    refuse('chart-appversion-mismatch', `operator chart version "${chart.chartVersion}" != kubernetes group version "${k.version}"`);
  }
}

// ---- ledger -------------------------------------------------------------
export function loadLedger(path = LEDGER_PATH) {
  if (!existsSync(path)) return { schema: 'pacto-release-ledger/v1', artifacts: {} };
  try { return JSON.parse(readFileSync(path, 'utf8')); } catch { return { schema: 'pacto-release-ledger/v1', artifacts: {} }; }
}
export function writeLedger(ledger, path = LEDGER_PATH) {
  writeFileSync(path, JSON.stringify(ledger, null, 2) + '\n');
}

// ---- publish ------------------------------------------------------------
export function publish({ env = process.env, dryRun = false, gitStatus = defaultGitStatus, ledgerPath = LEDGER_PATH } = {}) {
  const target = resolveTarget(env);
  const { manifest, plan } = loadReleaseState();

  // Preflight — fail-closed refusals (b,c,d,e). The dirty-tree gate (f) is a
  // publish-time guard, so it is skipped for a dry run (which publishes nothing).
  checkOwnership(manifest);                               // (b)
  const order = checkPublisherMapping(manifest, plan);    // (c,d)
  checkChartAppVersion(plan);                             // (e)
  if (!dryRun) checkCleanTree(env, gitStatus);            // (f)

  const counts = Object.fromEntries(STATES.map((s) => [s, 0]));

  if (dryRun) {
    const planned = order.map(({ unit }) => {
      const u = manifest.units[unit];
      return { unit, ref: stagingRef(target, u), contentDigest: sha256(unitContent(unit, u)) };
    });
    counts.planned = planned.length;
    return { schema: 'pacto-release-ledger/v1', target, dryRun: true, planned, counts };
  }

  const prior = loadLedger(ledgerPath);
  const ledger = { schema: 'pacto-release-ledger/v1', target, artifacts: {} };

  for (const { unit } of order) {
    const u = manifest.units[unit];
    const ref = stagingRef(target, u);
    const content = unitContent(unit, u);
    const contentDigest = sha256(content);
    const rec = { unit, kind: u.artifactKind, ref, contentDigest, state: 'planned' };
    ledger.artifacts[unit] = rec;
    counts.planned++;

    const priorRec = prior.artifacts?.[unit];
    if (priorRec && priorRec.state !== 'complete') { rec.resumedFrom = priorRec.state; counts.resumable++; }

    rec.state = 'built'; counts.built++;

    const remote = remoteLayerDigest(ref, target);
    rec.state = 'verified'; counts.verified++;
    if (remote) {
      if (remote === contentDigest) { rec.state = 'complete'; rec.result = 'already-published'; counts.complete++; continue; }
      // (a) immutable version already occupied by different bytes.
      rec.state = 'failed'; rec.remoteDigest = remote; counts.failed++; writeLedger(ledger, ledgerPath);
      refuse('immutable-version-violation',
        `${ref} is already published with a different digest (${remote}); refusing to overwrite immutable version with ${contentDigest}`);
    }

    try { pushArtifact(ref, target, content); }
    catch (e) {
      rec.state = 'failed'; rec.error = String(e.message || e); counts.failed++; writeLedger(ledger, ledgerPath);
      refuse('publish-failed', `failed to push ${ref}: ${rec.error}`);
    }
    rec.state = 'complete'; rec.result = 'published'; counts.published++; counts.complete++;
  }

  ledger.counts = counts;
  writeLedger(ledger, ledgerPath);
  return ledger;
}

function main() {
  const dryRun = process.argv.includes('--dry-run');
  try {
    const ledger = publish({ dryRun });
    console.log(`publish (${ledger.target})${dryRun ? ' [dry-run]' : ''}: ` +
      STATES.map((s) => `${s}=${ledger.counts[s] || 0}`).join(' '));
  } catch (e) {
    if (e instanceof PublishError) { console.error(`publish REFUSED [${e.code}]: ${e.message}`); process.exit(1); }
    throw e;
  }
}
if (import.meta.url === `file://${process.argv[1]}`) main();
