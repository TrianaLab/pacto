// Tests for the release decision + recovery logic.
// Run: node --test release/orchestrator/detect.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { createHash } from 'node:crypto';
import { decideRelease, decideRecovery } from './detect.mjs';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');

const stable = (v) => Array.isArray(v) ? v.map(stable)
  : (v && typeof v === 'object'
    ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, stable(v[k])])) : v);

// A ready transaction fixture with an internally consistent manifestSha + id.
function txn(over = {}) {
  const changedUnits = over.changedUnits ?? ['core', 'cli', 'dashboard-image'];
  const newVersions = over.newVersions ?? { core: '2.8.0', 'k8s-module': '4.7.0' };
  const previousVersions = over.previousVersions ?? { core: '2.7.0', 'k8s-module': '4.7.0' };
  const manifestSha = createHash('sha256').update(JSON.stringify(stable(newVersions))).digest('hex');
  const transactionId = changedUnits.length
    ? createHash('sha256').update(JSON.stringify(stable({ changedUnits, newVersions, previousVersions }))).digest('hex').slice(0, 16)
    : '';
  return {
    schema: 'pacto-release-transaction/v1',
    ready: changedUnits.length > 0,
    transactionId, sourceSha: '', manifestSha,
    changedGroups: over.changedGroups ?? ['core'],
    changedUnits, previousVersions, newVersions,
    expectedTags: {}, expectedCoordinates: {}, dependencyOrder: [],
    units: Object.fromEntries(changedUnits.map((u) => [u, { status: over.status?.[u] ?? 'pending' }])),
    ...over.raw,
  };
}

// ---- decideRelease ----

test('feature merge (not-ready transaction) publishes nothing', () => {
  const t = txn({ changedUnits: [], changedGroups: [] });
  assert.equal(decideRelease(t, t).release, false);
});

test('null / invalid transaction fails closed', () => {
  assert.equal(decideRelease(null, null).release, false);
  assert.equal(decideRelease({ schema: 'other' }, null).release, false);
  assert.equal(decideRelease({ schema: 'pacto-release-transaction/v1' }, null).release, false);
});

test('ready transaction newly introduced by this commit releases', () => {
  const t = txn();
  const prev = txn({ changedUnits: [], changedGroups: [] }); // previous commit: no release
  const d = decideRelease(t, prev);
  assert.equal(d.release, true);
  assert.deepEqual(d.changedUnits, ['core', 'cli', 'dashboard-image']);
  assert.deepEqual(d.changedGroups, ['core']);
});

test('ready transaction released with no previous transaction still releases', () => {
  assert.equal(decideRelease(txn(), null).release, true);
});

test('unchanged transaction on a later commit does NOT re-release', () => {
  const t = txn();
  assert.equal(decideRelease(t, t).release, false, 'same transactionId => already handled');
});

// ---- component selection matrix ----

const CORE = ['core', 'cli', 'dashboard-image', 'dashboard-contract-bundle', 'demo-bundles', 'demo-compose'];
const K8S = ['k8s-module', 'operator-image', 'operator-chart', 'k8s-docs'];

test('core-only release selects only the core group', () => {
  const d = decideRelease(txn({ changedUnits: CORE, changedGroups: ['core'] }), null);
  assert.deepEqual(d.changedGroups, ['core']);
  assert.ok(d.changedUnits.every((u) => CORE.includes(u)));
  assert.ok(!d.changedUnits.some((u) => K8S.includes(u)));
});

test('kubernetes-only release selects only the kubernetes group', () => {
  const d = decideRelease(txn({ changedUnits: K8S, changedGroups: ['kubernetes'] }), null);
  assert.deepEqual(d.changedGroups, ['kubernetes']);
  assert.ok(d.changedUnits.every((u) => K8S.includes(u)));
});

test('coordinated release selects both groups', () => {
  const d = decideRelease(txn({ changedUnits: [...CORE, ...K8S], changedGroups: ['core', 'kubernetes'] }), null);
  assert.deepEqual(d.changedGroups, ['core', 'kubernetes']);
});

// ---- decideRecovery (workflow_dispatch) ----

test('recovery requires a transactionId input', () => {
  assert.equal(decideRecovery(txn(), {}).ok, false);
  assert.equal(decideRecovery(txn(), { sourceSha: 'x' }).ok, false);
});

test('recovery rejects a transactionId mismatch', () => {
  assert.equal(decideRecovery(txn(), { transactionId: 'deadbeef', sourceSha: 'x' }).ok, false);
});

test('recovery requires + validates sourceSha', () => {
  const t = txn({ raw: { sourceSha: 'abc123' } });
  assert.equal(decideRecovery(t, { transactionId: t.transactionId }).ok, false);
  assert.equal(decideRecovery(t, { transactionId: t.transactionId, sourceSha: 'wrong' }).ok, false);
  assert.equal(decideRecovery(t, { transactionId: t.transactionId, sourceSha: 'abc123' }).ok, true);
});

test('recovery rejects a manifestSha mismatch', () => {
  const t = txn();
  const r = decideRecovery(t, { transactionId: t.transactionId, sourceSha: 'x' }, { core: '9.9.9' });
  assert.equal(r.ok, false);
});

test('recovery rejects units not in the plan / adding new units', () => {
  const t = txn();
  const r = decideRecovery(t, { transactionId: t.transactionId, sourceSha: 'abc123', units: ['operator-image'] });
  assert.equal(r.ok, false);
});

// Completion is NOT read from the committed transaction file (production never
// updates units[*].status there). Recovery dispatches the requested units; each
// publisher's durable-ledger check skips the ones already recorded (identical), so
// recovery re-runs only the incomplete units, enforced fail-closed at publish time.
test('recovery dispatches the changed units (completion enforced at publish time)', () => {
  const t = txn(); // changedUnits = [core, cli, dashboard-image]
  const r = decideRecovery(t, { transactionId: t.transactionId, sourceSha: 'abc123' });
  assert.equal(r.ok, true);
  assert.deepEqual(r.units.sort(), ['cli', 'core', 'dashboard-image']);
});

test('recovery narrows to a requested subset of the plan', () => {
  const t = txn();
  const r = decideRecovery(t, { transactionId: t.transactionId, sourceSha: 'abc123', units: ['cli', 'core'] });
  assert.equal(r.ok, true);
  assert.deepEqual(r.units.sort(), ['cli', 'core']);
});

// ---- item 1 regression: the COMMITTED transaction must publish nothing ----

test('committed release-transaction.json releases nothing on an unchanged merge (feature-PR safe)', () => {
  const committed = JSON.parse(readFileSync(join(root, 'release', 'release-transaction.json'), 'utf8'));
  // The real safety invariant: merging a PR that does not CHANGE the committed
  // transaction (HEAD and HEAD^ carry the same one) publishes nothing. This holds
  // for the empty pre-release state AND the already-consumed post-release state
  // (a ready transaction whose tags are published never re-releases when unchanged).
  // Asserting `.ready === false` / `changedUnits === []` here was a pre-release-only
  // proxy that wrongly fails in the window right after a release merges to main.
  assert.equal(decideRelease(committed, committed).release, false);
});
