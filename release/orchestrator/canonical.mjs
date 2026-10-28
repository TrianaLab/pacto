// canonical.mjs — the one canonical JSON form behind every release hash.
// build-release-plan.mjs produces manifestSha with it, detect.mjs checks it,
// and dry-run.sh imports it, so producer and consumer cannot drift.
// (release.yml's ledger step restates it in jq so a recovery of an older
// source_sha never depends on this file existing there.)
import { createHash } from 'node:crypto';

// stable deep-sorts object keys so JSON.stringify is byte-identical.
export const stable = (v) => Array.isArray(v) ? v.map(stable)
  : (v && typeof v === 'object'
    ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, stable(v[k])])) : v);

export const sha256Stable = (obj) => createHash('sha256').update(JSON.stringify(stable(obj))).digest('hex');

// manifestSha hashes the {unit: version} map a release transaction records.
export const manifestSha = (versions) => sha256Stable(versions);
