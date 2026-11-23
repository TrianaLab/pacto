import { describe, it, expect } from 'vitest';
import { clusterWord, clusterReason, clusterCaptionParts, showClusters, approveCommand, isDNSLabel, shortKey, joinNameTip, joinHostsTip, approvedWords, removedWords, snapshotWords, joinCounts, candidateGroups, candidateWord, candidateTip, candidateLabels, hiddenCandidateCount, type ClusterSummary } from './clusters.ts';
import type { ClusterCandidate } from './api.ts';

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

describe('candidate clusters', () => {
  const cand = (over: Partial<ClusterCandidate>): ClusterCandidate =>
    ({ source: 'argocd', kind: 'Secret', namespace: 'argocd', name: 'eu-prod', match: 'unmatched', ...over });

  it('groups candidates by source, with an install only for argocd and flux', () => {
    const g = candidateGroups([cand({ source: 'capi', kind: 'Cluster', name: 'c' }), cand({}), cand({ source: 'flux', name: 'f' }), cand({ name: 'us' })]);
    expect(g.map((x) => [x.source, x.label, x.install, x.action, x.items.length])).toEqual([
      ['argocd', 'Argo CD', 'argocd', 'Get ApplicationSet', 2], ['flux', 'Flux', 'flux', 'Get Flux manifests', 1], ['capi', 'Cluster API', null, '', 1]]);
    expect(candidateGroups([])).toEqual([]);
  });

  it('never says not joined', () => {
    const words = [cand({}), cand({ match: 'same-uid', uid: '9f1c4e2a-1' }), cand({ match: 'matched', member: { uid: 'u', name: 'eu-prod' } }), cand({ match: 'matched', member: { uid: 'u' } })].map(candidateWord);
    expect(words).toEqual(['not matched', 'states the same UID', 'matched to eu-prod', 'matched']);
    expect(words.join(' ')).not.toMatch(/joined|member of|not a member/);
  });

  it('keeps the uid out of every label and names it with its source in the tip', () => {
    const uid = '9f1c4e2a-0000-4000-8000-000000000001';
    const c = cand({ match: 'matched', uid, uidSource: 'annotation pacto.trianalab.io/cluster-uid', member: { uid, name: 'eu-prod' }, labels: { env: 'prod' } });
    expect(candidateWord(c) + candidateLabels(c.labels).shown.join(' ')).not.toContain(uid.slice(0, 8));
    expect(candidateTip(c)).toBe(`From Argo CD Secret argocd/eu-prod. States UID ${uid} (annotation pacto.trianalab.io/cluster-uid). The approved member eu-prod publishes that UID under its approved key.`);
    expect(candidateTip(cand({ source: 'rancher', kind: 'Cluster', namespace: undefined, name: 'c-1', reason: 'the candidate states no cluster UID' })))
      .toBe('From Rancher Cluster c-1. The candidate states no cluster UID.');
    expect(candidateTip(cand({ match: 'same-uid', uid: 'x', reason: 'a person has not approved it' }))).toBe('From Argo CD Secret argocd/eu-prod. States UID x. A person has not approved it.');
    expect(candidateTip(cand({ match: 'matched', member: { uid: 'u' } }))).toContain('The approved member publishes that UID');
  });

  it('labels collapse after three with the rest in the title', () => {
    expect(candidateLabels({ region: 'eu', env: 'prod', tier: 'a', b: '1' })).toEqual({ shown: ['b=1', 'env=prod', 'region=eu'], more: 1, title: 'b=1, env=prod, region=eu, tier=a' });
    expect(candidateLabels(undefined)).toEqual({ shown: [], more: 0, title: '' });
  });

  it('hidden candidates counted', () => {
    expect(hiddenCandidateCount(0)).toBe('');
    expect(hiddenCandidateCount(1)).toBe('1 candidate cluster hidden: they show to those who may approve clusterjoins');
    expect(hiddenCandidateCount(3)).toBe('3 candidate clusters hidden: they show to those who may approve clusterjoins');
  });
});
