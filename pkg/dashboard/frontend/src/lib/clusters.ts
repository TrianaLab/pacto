// Per-cluster status words for meta.clusters: the Cluster filter caption on the
// Services and Targets lists and the Clusters block on Data sources, plus the
// member join rows (514-S3) that block shows.
import type { ClusterSummary, ClusterJoinState } from './api.ts';

export type { ClusterSummary };

/** Where a cluster's display name came from, in words. */
export const CLUSTER_NAME_SOURCES: Record<string, string> = { approval: 'approval', 'node-label': 'node label', context: 'context name', file: 'target-state file', uid: 'cluster id' };

const ACCESS_WORDS: Record<string, string> = { 'no-access': 'no access', 'review-failed': 'review failed', 'not-reviewable': 'not reviewable' };
const STATUS_WORDS: Record<string, string> = { available: 'complete', partial: 'partial', stale: 'stale', unavailable: 'unreachable' };

const REASONS: Record<string, string> = {
  complete: 'Every data source that reads this cluster answered.',
  partial: 'A data source that reads this cluster answered only in part, so its counts may be low.',
  stale: 'Pacto could not reach this cluster and shows what it read last.',
  unreachable: 'Pacto could not reach this cluster and has no earlier read, so its services are unknown.',
  'no access': 'You may read no namespace in this cluster, so its services are hidden.',
  'review failed': 'The access review in this cluster failed or took longer than 5s, so its services are hidden until it answers.',
  'not reviewable': 'Pacto cannot review you in this cluster, so its services are hidden.',
};

// The words of a cluster that shows nothing: an access mark or an unreachable cluster.
const HIDES = new Set(['no access', 'review failed', 'not reviewable', 'unreachable']);

/** clusterWord turns a cluster's status and access into one word; access wins. */
export function clusterWord(s: ClusterSummary): string {
  return (s.access && ACCESS_WORDS[s.access]) || STATUS_WORDS[s.status] || s.status;
}

/** clusterReason is the sentence behind clusterWord, for a HelpTip. */
export function clusterReason(s: ClusterSummary): string {
  return REASONS[clusterWord(s)];
}

/** showClusters: the filter and the block appear with two clusters or any access mark. */
export function showClusters(clusters: ClusterSummary[] | null | undefined): boolean {
  return !!clusters && (clusters.length >= 2 || clusters.some((c) => c.access));
}

/** clusterCaptionParts: the caption's head and the clusters it names as hidden, for markup.
 *  truncated (meta.clustersTruncated) says more clusters exist than the answer lists, so the
 *  head never says "all". */
export function clusterCaptionParts(clusters: ClusterSummary[], selected: string, truncated = false): { head: string; hidden: { name: string; word: string }[] } {
  const hidden = clusters.filter((c) => HIDES.has(clusterWord(c)));
  const shown = clusters.filter((c) => !hidden.includes(c) && (!selected || c.id === selected)).length;
  const head = truncated
    ? `Showing ${shown} of the ${clusters.length} listed clusters; more are not listed`
    : !selected && !hidden.length ? `Showing all ${clusters.length} clusters` : `Showing ${shown} of ${clusters.length} clusters`;
  return { head, hidden: hidden.map((c) => ({ name: c.name || c.id, word: clusterWord(c) })) };
}

/** isDNSLabel: an RFC 1123 label, the only form a member display name takes. */
export function isDNSLabel(s: string): boolean {
  return s.length <= 63 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(s);
}

// shellQuote leaves a plain word alone and single-quotes anything else, so a copied
// command never runs what a value carries.
function shellQuote(s: string): string {
  return /^[A-Za-z0-9._:/-]+$/.test(s) ? s : `'${s.replaceAll("'", `'\\''`)}'`;
}

/** approveCommand is the command that approves a member: it names the key the person
 *  checked (--key), when given the display name (--name), and the registry the hub read
 *  (--registry), without which the command does not run. */
export function approveCommand(uid: string, name: string, keyID: string, registry: string): string {
  const parts = ['pacto cluster approve', shellQuote(uid), '--key', shellQuote(`sha256:${keyID}`)];
  if (name) parts.push('--name', shellQuote(name));
  if (registry) parts.push('--registry', shellQuote(registry));
  return parts.join(' ');
}

/** shortKey is a key fingerprint cut to its head and tail (sha256:91ab…3e7f2); the title holds it whole. */
export function shortKey(keyID: string): string {
  return keyID.length > 12 ? `sha256:${keyID.slice(0, 4)}…${keyID.slice(-5)}` : `sha256:${keyID}`;
}

/** joinHostsTip says where each advertised environment came from: they are inferred. */
export function joinHostsTip(r: ClusterJoinState): string {
  return `Inferred, used by nothing yet: ${(r.environments ?? []).map((e) => `${e.name} from ${e.source}`).join('; ')}.`;
}

/** joinNameTip says where a member's display name came from. */
export function joinNameTip(r: ClusterJoinState): string {
  return `Name from the ${CLUSTER_NAME_SOURCES[r.nameSource ?? 'uid'] ?? CLUSTER_NAME_SOURCES.uid}.`;
}

/** approvedWords: "Approved by oidc:alice@acme.com on 2026-11-20." for a member's HelpTip. */
export function approvedWords(r: ClusterJoinState | undefined): string {
  if (!r?.approver) return '';
  return ` Approved by ${r.approver}${r.approvedAt ? ` on ${r.approvedAt.slice(0, 10)}` : ''}.`;
}

/** removedWords: "Removed by oidc:bob@acme.com on 2026-11-20." for a removed member. */
export function removedWords(r: ClusterJoinState): string {
  if (!r.approver) return '';
  return `Removed by ${r.approver}${r.approvedAt ? ` on ${r.approvedAt.slice(0, 10)}` : ''}.`;
}

/** snapshotWords: how old a pending cluster's snapshot is when it is not fresh, so a
 *  person never approves from stale evidence; '' when it is. */
export function snapshotWords(r: ClusterJoinState): string {
  if (!r.status || r.status === 'available') return '';
  const word = STATUS_WORDS[r.status] ?? r.status;
  return r.snapshotAsOf ? `snapshot ${word}, from ${r.snapshotAsOf.slice(0, 16).replace('T', ' ')}` : `snapshot ${word}`;
}

/** joinCounts: the rows a reader cannot see and the records the list did not use.
 *  reviewFailed says the hub could not check whether the reader may approve: then the
 *  rows are hidden for an unknown reason, never a denial. */
export function joinCounts(hidden: number, otherHubs: number, reviewFailed = false): string[] {
  const out: string[] = [];
  const clusters = `${hidden} cluster${hidden === 1 ? '' : 's'}`;
  if (hidden) out.push(reviewFailed ? `${clusters} hidden: whether you may approve them could not be checked` : `${clusters} you may not approve`);
  if (otherHubs) out.push(`${otherHubs} record${otherHubs === 1 ? '' : 's'} from another hub not used`);
  return out;
}
