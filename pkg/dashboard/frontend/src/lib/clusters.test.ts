import { describe, it, expect } from 'vitest';
import { clusterWord, clusterReason, clusterCaptionParts, showClusters, type ClusterSummary } from './clusters.ts';

const c = (id: string, name: string, status: ClusterSummary['status'] = 'available', access?: ClusterSummary['access']): ClusterSummary =>
  ({ id, name, status, targets: 1, ...(access ? { access } : {}) });

describe('clusterWord', () => {
  it('turns status and access into one word, access first', () => {
    expect(clusterWord(c('a', 'a'))).toBe('complete');
    expect(clusterWord(c('a', 'a', 'partial'))).toBe('partial');
    expect(clusterWord(c('a', 'a', 'stale'))).toBe('stale');
    expect(clusterWord(c('a', 'a', 'unavailable'))).toBe('unreachable');
    expect(clusterWord(c('a', 'a', 'available', 'no-access'))).toBe('no access');
    expect(clusterWord(c('a', 'a', 'stale', 'review-failed'))).toBe('review failed');
    expect(clusterWord(c('a', 'a', 'available', 'not-reviewable'))).toBe('not reviewable');
  });

  it('gives every word a reason', () => {
    for (const s of [c('a', 'a'), c('a', 'a', 'partial'), c('a', 'a', 'stale'), c('a', 'a', 'unavailable'),
      c('a', 'a', 'available', 'no-access'), c('a', 'a', 'available', 'review-failed'), c('a', 'a', 'available', 'not-reviewable')]) {
      expect(clusterReason(s)).toMatch(/\.$/);
    }
    expect(clusterReason(c('a', 'a', 'available', 'review-failed'))).toContain('5s');
  });
});

// caption joins the parts as ClusterFilter's markup does.
const caption = (clusters: ClusterSummary[], selected: string, truncated = false): string => {
  const { head, hidden } = clusterCaptionParts(clusters, selected, truncated);
  return hidden.length ? `${head} (${hidden.map((h) => `${h.name}: ${h.word}`).join(', ')})` : head;
};

describe('clusterCaptionParts', () => {
  const two = [c('eu', 'eu-prod'), c('us', 'us-prod', 'available', 'no-access')];

  it('names the hidden cluster and its reason', () => {
    expect(caption(two, 'eu')).toBe('Showing 1 of 2 clusters (us-prod: no access)');
    expect(caption(two, '')).toBe('Showing 1 of 2 clusters (us-prod: no access)');
  });

  it('counts an unreachable cluster as hidden and a stale one as shown', () => {
    const three = [c('eu', 'eu-prod'), c('us', 'us-prod', 'unavailable'), c('ap', 'ap-prod', 'stale')];
    expect(caption(three, '')).toBe('Showing 2 of 3 clusters (us-prod: unreachable)');
    expect(caption(three, 'ap')).toBe('Showing 1 of 3 clusters (us-prod: unreachable)');
  });

  it('says all when nothing is hidden or selected', () => {
    expect(caption([c('eu', 'eu-prod'), c('us', 'us-prod')], '')).toBe('Showing all 2 clusters');
  });

  it('never says all when the answer lists only some clusters', () => {
    expect(caption([c('eu', 'eu-prod'), c('us', 'us-prod')], '', true)).toBe('Showing 2 of the 2 listed clusters; more are not listed');
  });

  it('reads 0 when the selected cluster is hidden', () => {
    expect(caption(two, 'us')).toBe('Showing 0 of 2 clusters (us-prod: no access)');
  });
});

describe('showClusters', () => {
  it('shows with two clusters or any access mark', () => {
    expect(showClusters(undefined)).toBe(false);
    expect(showClusters([c('eu', 'eu-prod')])).toBe(false);
    expect(showClusters([c('eu', 'eu-prod', 'available', 'no-access')])).toBe(true);
    expect(showClusters([c('eu', 'eu-prod'), c('us', 'us-prod')])).toBe(true);
  });
});
