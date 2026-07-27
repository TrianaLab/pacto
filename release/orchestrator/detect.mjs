#!/usr/bin/env node
// detect.mjs — decide whether a commit releases, and which units, from the
// release transaction. Fail-closed: any
// missing/invalid/ambiguous input => release nothing.
//
// Two decisions:
//   decideRelease(txn, prevTxn)  — push to main. Releases only when a READY
//     transaction with a non-empty changedUnits set is NEWLY introduced by this
//     commit (its transactionId differs from the previous commit's). A feature
//     merge carries the previous release's stale ready transaction unchanged, so
//     it never re-fires.
//   decideRecovery(txn, input, manifestVersions) — workflow_dispatch. A recovery
//     mechanism, never a second release trigger: it validates an existing
//     transaction and rejects anything that would publish something new.
import { readFileSync, appendFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const SCHEMA = 'pacto-release-transaction/v1';

const stable = (v) => Array.isArray(v) ? v.map(stable)
  : (v && typeof v === 'object'
    ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, stable(v[k])])) : v);

function valid(txn) {
  return !!txn && txn.schema === SCHEMA && Array.isArray(txn.changedUnits);
}

export function decideRelease(txn, prevTxn) {
  if (!valid(txn)) return { release: false, reason: 'no valid release transaction' };
  if (!txn.ready || txn.changedUnits.length === 0) {
    return { release: false, reason: 'transaction not ready / no changed units' };
  }
  // Changed-in-commit guard: only the commit that introduces this exact
  // transaction publishes it. A later commit carrying the same transaction
  // unchanged (a feature merge after a release) does not re-release.
  if (valid(prevTxn) && prevTxn.ready && prevTxn.transactionId === txn.transactionId) {
    return { release: false, reason: 'transaction unchanged since previous commit' };
  }
  return {
    release: true,
    reason: 'ready transaction introduced by this commit',
    transactionId: txn.transactionId,
    changedGroups: txn.changedGroups,
    changedUnits: txn.changedUnits,
    dependencyOrder: txn.dependencyOrder,
  };
}

export function decideRecovery(txn, input, manifestVersions) {
  if (!valid(txn)) return { ok: false, reason: 'no valid release transaction' };
  if (!txn.ready || txn.changedUnits.length === 0) {
    return { ok: false, reason: 'transaction is not a ready release' };
  }
  if (!input || !input.transactionId) {
    return { ok: false, reason: 'workflow_dispatch requires a transactionId input' };
  }
  if (input.transactionId !== txn.transactionId) {
    return { ok: false, reason: `transactionId mismatch: input ${input.transactionId} != ${txn.transactionId}` };
  }
  if (!input.sourceSha) {
    return { ok: false, reason: 'workflow_dispatch requires a sourceSha input' };
  }
  if (txn.sourceSha && input.sourceSha !== txn.sourceSha) {
    return { ok: false, reason: `sourceSha mismatch: input ${input.sourceSha} != ${txn.sourceSha}` };
  }
  // manifest must match the transaction's recorded shape (poisoned/rolled manifest).
  if (manifestVersions) {
    const got = createHash('sha256').update(JSON.stringify(stable(manifestVersions))).digest('hex');
    if (got !== txn.manifestSha) {
      return { ok: false, reason: 'manifestSha mismatch: manifest does not match the transaction' };
    }
  }
  // A requested subset (optional) may only narrow to units already in the plan,
  // and never add new units.
  let units = txn.changedUnits;
  if (Array.isArray(input.units) && input.units.length) {
    const foreign = input.units.filter((u) => !txn.changedUnits.includes(u));
    if (foreign.length) {
      return { ok: false, reason: `units not in the transaction plan: ${foreign.join(',')}` };
    }
    units = input.units;
  }
  // Recovery only touches units NOT already complete.
  const recoverable = units.filter((u) => (txn.units?.[u]?.status ?? 'pending') !== 'complete');
  if (recoverable.length === 0) {
    return { ok: false, reason: 'nothing to recover: all requested units already complete' };
  }
  return { ok: true, reason: 'recovery of incomplete units', transactionId: txn.transactionId, units: recoverable };
}

// CLI entry for release.yml's detect job. On push it decides from the transaction
// at HEAD vs HEAD^; on workflow_dispatch it validates a recovery request. Emits
// GITHUB_OUTPUT (release, recover, units, units_json, groups, transaction_id,
// source_sha). A refused recovery exits non-zero; a normal "no release" push exits 0.
function readTxn(ref) {
  try {
    return JSON.parse(ref === 'HEAD'
      ? readFileSync('release/release-transaction.json', 'utf8')
      : execFileSync('git', ['show', `${ref}:release/release-transaction.json`], { encoding: 'utf8' }));
  } catch { return null; }
}

function readManifestVersions() {
  try {
    const m = JSON.parse(readFileSync('release/release-manifest.json', 'utf8'));
    return Object.fromEntries(Object.entries(m.units || {}).map(([u, v]) => [u, v.version]));
  } catch { return null; }
}

function emit(o) {
  const lines = [
    `release=${!!o.release}`,
    `recover=${!!o.recover}`,
    `units=${(o.units || []).join(',')}`,
    `units_json=${JSON.stringify(o.units || [])}`,
    `groups=${(o.groups || []).join(',')}`,
    `transaction_id=${o.transaction_id || ''}`,
    `source_sha=${o.source_sha || ''}`,
  ].join('\n') + '\n';
  // Always write the decision to stdout (visible in the job log and readable by
  // the staging/version test harnesses); ADDITIONALLY append to GITHUB_OUTPUT for
  // the real release.yml detect job. GITHUB_OUTPUT is set on EVERY GitHub Actions
  // step, so a stdout-only-when-unset branch would silently starve those harnesses
  // of output in CI.
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, lines);
  process.stdout.write(lines);
}

function main() {
  const event = process.env.GITHUB_EVENT_NAME || 'push';
  if (event === 'workflow_dispatch') {
    const input = {
      transactionId: process.env.INPUT_TRANSACTION_ID,
      sourceSha: process.env.INPUT_SOURCE_SHA,
      units: (process.env.INPUT_UNITS || '').split(',').map((s) => s.trim()).filter(Boolean),
    };
    const r = decideRecovery(readTxn('HEAD'), input, readManifestVersions());
    if (!r.ok) {
      console.error(`detect: recovery REFUSED — ${r.reason}`);
      emit({ release: false, recover: false });
      process.exit(1);
    }
    console.error(`detect: RECOVERY of [${r.units.join(',')}] — ${r.reason}`);
    emit({ release: true, recover: true, units: r.units, transaction_id: r.transactionId, source_sha: input.sourceSha });
    return;
  }
  const d = decideRelease(readTxn('HEAD'), readTxn('HEAD^'));
  console.error(`detect: ${d.release ? 'RELEASE' : 'no release'} — ${d.reason}`);
  emit({
    release: d.release, recover: false,
    units: d.changedUnits || [], groups: d.changedGroups || [],
    transaction_id: d.transactionId || '', source_sha: process.env.GITHUB_SHA || '',
  });
}

if (import.meta.url === `file://${process.argv[1]}`) main();
