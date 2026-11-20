// Per-cluster status words for meta.clusters: the Cluster filter caption on the
// Services and Targets lists and the Clusters block on Data sources.
import type { ClusterSummary } from './api.ts';

export type { ClusterSummary };

const ACCESS_WORDS: Record<string, string> = { 'no-access': 'no access', 'review-failed': 'review failed', 'not-reviewable': 'not reviewable' };
const STATUS_WORDS: Record<string, string> = { available: 'complete', partial: 'partial', stale: 'stale', unavailable: 'unreachable' };

const REASONS: Record<string, string> = {
  complete: 'Every data source that reads this cluster answered.',
  partial: 'A data source that reads this cluster answered only in part, so its counts may be low.',
  stale: 'Pacto could not reach this cluster and shows what it read last.',
  unreachable: 'Pacto could not reach this cluster and has no earlier read, so its services are unknown.',
  'no access': 'You may read no namespace in this cluster, so its services are hidden.',
  'review failed': 'The access review in this cluster failed or took longer than 5s, so its services are hidden until it answers.',
  'not reviewable': 'Pacto cannot review signed-in readers in this cluster, so its services are hidden from them.',
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

/** clusterCaptionParts: the caption's head and the clusters it names as hidden, for markup. */
export function clusterCaptionParts(clusters: ClusterSummary[], selected: string): { head: string; hidden: { name: string; word: string }[] } {
  const hidden = clusters.filter((c) => HIDES.has(clusterWord(c)));
  const shown = clusters.filter((c) => !hidden.includes(c) && (!selected || c.id === selected)).length;
  return {
    head: !selected && !hidden.length ? `Showing all ${clusters.length} clusters` : `Showing ${shown} of ${clusters.length} clusters`,
    hidden: hidden.map((c) => ({ name: c.name || c.id, word: clusterWord(c) })),
  };
}

/** clusterCaption: "Showing 1 of 2 clusters (us-prod: no access)". */
export function clusterCaption(clusters: ClusterSummary[], selected: string): string {
  const { head, hidden } = clusterCaptionParts(clusters, selected);
  return hidden.length ? `${head} (${hidden.map((h) => `${h.name}: ${h.word}`).join(', ')})` : head;
}
