#!/usr/bin/env node --test
// Tests for the staging release publisher.
//
//   * Refusal unit tests (deterministic, no registry): each of the six
//     fail-closed refusals fires with its distinct error code.
//   * Registry integration tests (skip when oras / a local registry is
//     unavailable): idempotent skip on rerun, immutable-version-violation on
//     different bytes at the same version, and resume from a non-complete ledger.
//
// Proof of each is captured to release/proofs/publish-*.txt.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import {
  PublishError, resolveTarget, checkOwnership, checkPublisherMapping, checkChartAppVersion,
  checkCleanTree, resolveOrder, publish, pushArtifact, remoteLayerDigest, stagingRef,
  unitContent, sha256, loadReleaseState, loadLedger, writeLedger, STATES,
} from './publish.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const proofs = join(here, '..', 'proofs');
const proof = (name, body) => writeFileSync(join(proofs, name), body.endsWith('\n') ? body : body + '\n');

function refusal(fn, code) {
  assert.throws(fn, (e) => e instanceof PublishError && e.code === code, `expected refusal code ${code}`);
  try { fn(); return ''; } catch (e) { return `[${e.code}] ${e.message}`; }
}

// ---------------------------------------------------------------- refusals
test('six fail-closed refusals each fire with a distinct code', () => {
  const lines = ['# PROOF - publish.mjs fail-closed refusals (each a distinct, tested code)', ''];

  // (a) immutable-version-violation is proved live below; record its contract here.
  // (b) duplicate artifact ownership: two units claim one coordinate.
  lines.push('(b) duplicate-artifact-ownership:');
  lines.push('  ' + refusal(() => checkOwnership({ units: {
    'dashboard-image': { coordinate: 'ghcr.io/x/dash', artifactKind: 'oci-image' },
    'other': { coordinate: 'ghcr.io/x/dash', artifactKind: 'oci-image' },
  } }), 'duplicate-artifact-ownership'), '');

  // (c) a release unit with no publisher mapping.
  lines.push('(c) unit-without-publisher:');
  lines.push('  ' + refusal(() => checkPublisherMapping(
    { units: { orphan: { coordinate: 'ghcr.io/x/o', artifactKind: 'oci-image' } } },
    { publishOrder: [], groups: {} }), 'unit-without-publisher'), '');

  // (d) a publisher with no release unit (both the unresolved-step and the
  //     resolved-but-absent-from-manifest branches).
  lines.push('(d) publisher-without-unit (unresolved step):');
  lines.push('  ' + refusal(() => checkPublisherMapping(
    { units: {} },
    { publishOrder: ['core:bogus'], groups: { core: { artifacts: [] } } }), 'publisher-without-unit'));
  lines.push('(d) publisher-without-unit (unit not in manifest):');
  lines.push('  ' + refusal(() => checkPublisherMapping(
    { units: {} },
    { publishOrder: ['core:go-module'], groups: { core: { artifacts: [{ unit: 'core', kind: 'go-module' }] } } }),
    'publisher-without-unit'), '');

  // (e) chart version != image appVersion.
  const badApp = { groups: { kubernetes: { version: '4.7.0', artifacts: [
    { unit: 'operator-image', kind: 'oci-image', tag: '4.7.0' },
    { unit: 'operator-chart', kind: 'helm-chart', chartVersion: '4.7.0', appVersion: '4.7.1' },
  ] } } };
  lines.push('(e) chart-appversion-mismatch (appVersion != image):');
  lines.push('  ' + refusal(() => checkChartAppVersion(badApp), 'chart-appversion-mismatch'), '');

  // (f) release from a dirty git tree / untrusted source.
  lines.push('(f) dirty-tree:');
  lines.push('  ' + refusal(() => checkCleanTree({}, () => ' M integrations/kubernetes/go.mod'), 'dirty-tree'), '');

  // production-target guard (never publish to a prod coordinate).
  lines.push('(guard) production-target-refused:');
  lines.push('  ' + refusal(() => resolveTarget({ PACTO_STAGING_REGISTRY: 'ghcr.io/trianalab/pacto-dashboard' }),
    'production-target-refused'), '');

  proof('publish-refusals.txt', lines.join('\n'));
});

test('guards pass on the real release manifest + plan', () => {
  const { manifest, plan } = loadReleaseState();
  assert.doesNotThrow(() => checkOwnership(manifest));
  assert.doesNotThrow(() => checkPublisherMapping(manifest, plan));
  assert.doesNotThrow(() => checkChartAppVersion(plan));
  assert.doesNotThrow(() => checkCleanTree({ PACTO_STAGING_ALLOW_DIRTY: '1' }));
  // publishOrder resolves to all eight units in deterministic (core-first) order.
  assert.deepEqual(resolveOrder(plan).map((o) => o.unit),
    ['core', 'dashboard-image', 'cli', 'demo-bundles', 'k8s-module', 'operator-image', 'operator-chart', 'k8s-docs']);
});

test('resolveTarget defaults to a local disposable registry', () => {
  assert.equal(resolveTarget({}), 'localhost:5001');
  assert.equal(resolveTarget({ PACTO_STAGING_REGISTRY: 'ghcr.io/trianalab/x', PACTO_ALLOW_PROD: '1' }), 'ghcr.io/trianalab/x');
});

// ---------------------------------------------------------- registry-backed
const BASE = process.env.PACTO_STAGING_REGISTRY || 'localhost:5001';
function registryUsable() {
  try { execFileSync('oras', ['version'], { stdio: 'ignore' }); } catch { return false; }
  try { pushArtifact(`${BASE}/pacto-probe/ping:v0`, BASE, 'probe\n'); return true; } catch { return false; }
}
const USABLE = registryUsable();
const skip = USABLE ? false : 'oras or local registry unavailable';
const run = (env) => publish({ env: { ...env, PACTO_STAGING_ALLOW_DIRTY: '1' }, ledgerPath: env.__ledger });
const ns = (label) => `${BASE}/pacto-test-${label}-${Date.now()}`;
const tmpLedger = () => join(mkdtempSync(join(tmpdir(), 'pacto-ledger-')), 'ledger.json');
const line = (l) => STATES.map((s) => `${s}=${l.counts[s] || 0}`).join(' ');

test('idempotent: rerun skips already-published artifacts', { skip }, () => {
  const target = ns('idem'); const __ledger = tmpLedger();
  const first = run({ PACTO_STAGING_REGISTRY: target, __ledger });
  const second = run({ PACTO_STAGING_REGISTRY: target, __ledger });
  assert.equal(first.counts.published, 8, 'first run publishes all eight units');
  assert.equal(first.counts.complete, 8);
  assert.equal(second.counts.published, 0, 'rerun publishes nothing');
  assert.equal(second.counts.complete, 8, 'rerun sees all eight already-published');
  for (const r of Object.values(second.artifacts)) assert.equal(r.result, 'already-published');

  proof('publish-idempotency.txt',
    ['# PROOF - digest-aware idempotency (rerun = zero re-publish)', '',
      `target: ${target}`, '',
      `RUN 1 (fresh):  ${line(first)}`,
      `RUN 2 (rerun):  ${line(second)}   <- published=0, all already-published`, '',
      '# publish order (release-plan.publishOrder, core module first):',
      ...Object.values(first.artifacts).map((r) => `  ${r.state === 'complete' ? 'OK' : r.state} ${r.unit.padEnd(16)} ${r.ref}`),
    ].join('\n'));
  proof('publish-ledger.txt',
    '# PROOF - machine-readable release ledger (state counts + per-artifact)\n\n' +
    JSON.stringify(first, null, 2) + '\n');
});

test('immutable: different bytes at the same version are refused', { skip }, () => {
  const target = ns('immutable'); const __ledger = tmpLedger();
  run({ PACTO_STAGING_REGISTRY: target, __ledger });                    // publish correct bytes
  const { manifest } = loadReleaseState();
  const ref = stagingRef(target, manifest.units.core);
  pushArtifact(ref, target, 'tampered-different-bytes\n');             // overwrite same version
  const remote = remoteLayerDigest(ref, target);
  const expected = sha256(unitContent('core', manifest.units.core));

  let caught;
  try { run({ PACTO_STAGING_REGISTRY: target, __ledger }); }
  catch (e) { caught = e; }
  assert.ok(caught instanceof PublishError && caught.code === 'immutable-version-violation', 'expected immutable-version-violation');

  proof('publish-immutable-violation.txt',
    ['# PROOF - (a) immutable-version-violation: same version, different bytes -> REFUSE', '',
      `ref:            ${ref}`,
      `expected bytes: ${expected}`,
      `remote bytes:   ${remote}   <- different`, '',
      `refusal: [${caught.code}] ${caught.message}`].join('\n'));
});

test('resumable: rerun resumes from a non-complete ledger', { skip }, () => {
  const target = ns('resume'); const __ledger = tmpLedger();
  const first = run({ PACTO_STAGING_REGISTRY: target, __ledger });      // all published + complete
  // Simulate a crashed run: mark two units non-complete in the ledger while they
  // remain present on the registry.
  const led = loadLedger(__ledger);
  led.artifacts['operator-chart'].state = 'built';
  led.artifacts['k8s-docs'].state = 'verified';
  writeLedger(led, __ledger);
  const resumed = run({ PACTO_STAGING_REGISTRY: target, __ledger });

  assert.equal(resumed.counts.resumable, 2, 'two non-complete artifacts resumed');
  assert.equal(resumed.counts.published, 0, 'nothing re-pushed (all present)');
  assert.equal(resumed.counts.complete, 8);

  proof('publish-resume.txt',
    ['# PROOF - resumable: rerun resumes from the first non-complete ledger entry', '',
      `target: ${target}`, '',
      `RUN 1 (fresh):            ${line(first)}`,
      'crash simulated: operator-chart -> built, k8s-docs -> verified (still on registry)',
      `RUN 2 (resume + verify):  ${line(resumed)}   <- resumable=2, published=0, complete=8`].join('\n'));
});
