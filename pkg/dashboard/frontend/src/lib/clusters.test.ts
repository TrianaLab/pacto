import { describe, it, expect } from 'vitest';
import { clusterWord, clusterReason, clusterCaptionParts, showClusters, approveCommand, isDNSLabel, shortKey, joinNameTip, joinHostsTip, approvedWords, removedWords, snapshotWords, joinCounts, type ClusterSummary } from './clusters.ts';

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

describe('approveCommand', () => {
  const KEY = '91ab'.repeat(16);
  it('names the key the person checked', () => {
    expect(approveCommand('9f1c4e2a', '', KEY, '')).toBe(`pacto cluster approve 9f1c4e2a --key sha256:${KEY}`);
    // The command runs as copied only with the registry the hub read (R3-1).
    expect(approveCommand('9f1c4e2a', '', KEY, 'ghcr.io/acme/pacto-targets')).toBe(`pacto cluster approve 9f1c4e2a --key sha256:${KEY} --registry ghcr.io/acme/pacto-targets`);
  });
  it('adds the display name, quoting anything that is not a plain word', () => {
    expect(approveCommand('9f1c4e2a', 'eu-prod', KEY, '')).toBe(`pacto cluster approve 9f1c4e2a --key sha256:${KEY} --name eu-prod`);
    expect(approveCommand('9f1c4e2a', "eu prod; rm -rf ~ 'x'", KEY, '')).toBe(`pacto cluster approve 9f1c4e2a --key sha256:${KEY} --name 'eu prod; rm -rf ~ '\\''x'\\'''`);
  });
});

describe('join row words', () => {
  it('checks a DNS label', () => {
    expect(isDNSLabel('eu-prod')).toBe(true);
    expect(isDNSLabel('EU-prod')).toBe(false);
    expect(isDNSLabel('-eu')).toBe(false);
    expect(isDNSLabel('a'.repeat(64))).toBe(false);
  });
  it('cuts a key short and says where names and hosts came from', () => {
    expect(shortKey('91ab0c'.padEnd(64, '7') + 'e7')).toBe('sha256:91ab…777e7');
    expect(shortKey('91ab0c')).toBe('sha256:91ab0c');
    const r = { uid: 'u', state: 'pending' as const, nameSource: 'node-label' as const, environments: [{ name: 'production', source: 'the cluster name eu-prod' }] };
    expect(joinNameTip(r)).toBe('Name from the node label.');
    expect(joinNameTip({ uid: 'u', state: 'pending' })).toBe('Name from the cluster id.');
    expect(joinHostsTip(r)).toBe('Inferred, used by nothing yet: production from the cluster name eu-prod.');
  });
  it('words an approval and the counts', () => {
    expect(approvedWords({ uid: 'u', state: 'approved', approver: 'oidc:alice@acme.com', approvedAt: '2026-11-20T10:00:00Z' })).toBe(' Approved by oidc:alice@acme.com on 2026-11-20.');
    expect(approvedWords(undefined)).toBe('');
    expect(joinCounts(2, 1)).toEqual(['2 clusters you may not approve', '1 record from another hub not used']);
    expect(joinCounts(0, 0)).toEqual([]);
    // A review that failed hides rows for an unknown reason, never a denial (R5-2).
    expect(joinCounts(2, 0, true)).toEqual(['2 clusters hidden: whether you may approve them could not be checked']);
    // A removed member names who removed it and when (R2-7).
    expect(removedWords({ uid: 'u', state: 'removed', approver: 'oidc:bob@acme.com', approvedAt: '2026-11-20T10:00:00Z' })).toBe('Removed by oidc:bob@acme.com on 2026-11-20.');
    expect(removedWords({ uid: 'u', state: 'removed' })).toBe('');
    // A pending snapshot that is not fresh says so (R2-5).
    expect(snapshotWords({ uid: 'u', state: 'pending', status: 'stale', snapshotAsOf: '2026-11-17T10:00:00Z' })).toBe('snapshot stale, from 2026-11-17 10:00');
    expect(snapshotWords({ uid: 'u', state: 'pending', status: 'unavailable' })).toBe('snapshot unreachable');
    expect(snapshotWords({ uid: 'u', state: 'pending', status: 'available' })).toBe('');
  });
});
