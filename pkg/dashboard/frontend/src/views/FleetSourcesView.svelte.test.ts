/**
 * Data sources' Clusters block (514-S2): one chip per cluster with its status word and
 * the reason in its HelpTip, shown only with two or more clusters or an access mark.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { readFileSync } from 'node:fs';

const { entitiesFn, clustersFn, installFn, saveFn } = vi.hoisted(() => ({ entitiesFn: vi.fn(), clustersFn: vi.fn(), installFn: vi.fn(), saveFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a), fleetProviders: () => Promise.resolve({ providers: [] }), fleetClusters: () => clustersFn(), fleetClusterInstall: (from: string) => installFn(from) } };
});
vi.mock('../lib/saveFile.ts', () => ({ saveFile: (...a: unknown[]) => saveFn(...a) }));

// @ts-expect-error — Svelte component has no declaration file
import FleetSourcesView from './FleetSourcesView.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function resp(clusters?: any[]): any {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources: [], clusters },
    total: 1, count: 1, offset: 0, limit: 25, truncated: false,
    entities: [{ kind: 'source', key: 'k8s', label: 'k8s', href: '/fleet/sources/k8s' }],
  };
}

async function render(clusters?: unknown[], joins: unknown = null) {
  entitiesFn.mockResolvedValue(resp(clusters));
  if (joins instanceof Error) clustersFn.mockRejectedValue(joins); else clustersFn.mockResolvedValue(joins);
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(FleetSourcesView, { target, props: { refreshTick: 0 } });
  await vi.waitFor(() => expect(target.querySelector('[data-testid="source-list"]')).not.toBeNull());
  return { target, done: () => { unmount(c); target.remove(); } };
}

describe('FleetSourcesView Clusters block', () => {
  beforeEach(() => { entitiesFn.mockReset(); clustersFn.mockReset(); });

  it('is hidden with one cluster and no access mark', async () => {
    const { target, done } = await render([{ id: 'uid-eu', name: 'eu-prod', status: 'available', targets: 2 }]);
    expect(target.querySelector('[data-testid="source-clusters"]')).toBeNull();
    done();
  });

  it('lists every cluster with its word when there are two', async () => {
    const { target, done } = await render([
      { id: 'uid-eu', name: 'eu-prod', status: 'stale', targets: 2 },
      { id: 'uid-us', name: 'us-prod', status: 'available', access: 'review-failed', targets: 0 },
    ]);
    const chips = Array.from(target.querySelectorAll('[data-testid="source-cluster"]'));
    expect(chips.map((c) => c.querySelector('.sc-word')?.textContent)).toEqual(['stale', 'review failed']);
    expect(chips[1].querySelector('bdi')?.getAttribute('title')).toBe('us-prod');
    done();
  });

  it('carries the reason in the HelpTip', async () => {
    const { target, done } = await render([{ id: 'uid-us', name: 'us-prod', status: 'available', access: 'no-access', targets: 0 }]);
    const tip = target.querySelector('[data-testid="source-cluster"] [role="note"]');
    expect(tip?.textContent).toBe('You may read no namespace in this cluster, so its services are hidden.');
    done();
  });

  // 514-S3: the member join list inside the same block.
  const KEY = '91ab'.repeat(16);
  const one = [{ id: 'uid-hub', name: 'hub', status: 'available', targets: 2 }];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
  const joinList = (over: Record<string, unknown> = {}): any => ({
    hub: { uid: 'uid-hub', name: 'hub' }, repository: 'ghcr.io/acme/pacto-targets', asOf: '', completeness: 'complete', hidden: 0, otherHubs: 0, canApprove: true,
    clusters: [{ uid: '9f1c4e2a', name: 'eu-prod-2', nameSource: 'node-label', state: 'pending', keyID: KEY, reason: 'nobody approved this key yet',
      environments: [{ name: 'production', source: 'the cluster name eu-prod-2' }], mayReplace: { uid: '77aa', name: 'eu-prod' } }],
    ...over,
  });

  it('shows with one cluster plus a pending row: name, source mark, muted hosts, short key, reason and Approve', async () => {
    const { target, done } = await render(one, joinList());
    const row = await vi.waitFor(() => { const r = target.querySelector('[data-testid="cluster-join-row"]'); expect(r).not.toBeNull(); return r as HTMLElement; });
    expect(target.querySelectorAll('[data-testid="source-cluster"]')).toHaveLength(1);
    expect(row.querySelector('.sc-name')?.getAttribute('title')).toBe('eu-prod-2');
    expect(row.querySelector('.sc-key bdi')?.textContent).toBe('sha256:91ab…b91ab');
    expect(row.querySelector('.sc-key bdi')?.getAttribute('title')).toBe(`sha256:${KEY}`);
    expect(row.querySelector('.sc-hosts')?.textContent).toContain('hosts: production');
    expect(row.querySelector('[data-testid="cluster-join-replace"]')?.textContent).toBe('may replace eu-prod');
    expect(row.textContent).toContain('nobody approved this key yet');
    expect(row.querySelector('[data-testid="cluster-approve"]')).not.toBeNull();
    done();
  });

  it('shows no Approve to a reader who may not approve, and counts what they cannot see', async () => {
    const { target, done } = await render(one, joinList({ canApprove: false, hidden: 2, otherHubs: 1 }));
    const counts = await vi.waitFor(() => { const c = target.querySelector('[data-testid="cluster-join-counts"]'); expect(c).not.toBeNull(); return c as HTMLElement; });
    expect(counts.textContent).toBe('2 clusters you may not approve; 1 record from another hub not used.');
    expect(target.querySelector('[data-testid="cluster-approve"]')).toBeNull();
    done();
  });

  it('keeps removed rows collapsed and names who approved a member in its tip', async () => {
    const two = [...one, { id: 'uid-eu', name: 'eu-prod', status: 'available', targets: 3 }];
    const { target, done } = await render(two, joinList({ clusters: [
      { uid: 'uid-eu', name: 'eu-prod', state: 'approved', approver: 'oidc:alice@acme.com', approvedAt: '2026-11-20T09:00:00Z' },
      { uid: 'uid-old', name: 'eu-old', state: 'removed', approver: 'oidc:bob@acme.com', approvedAt: '2026-11-21T09:00:00Z' },
    ] }));
    const det = await vi.waitFor(() => { const d = target.querySelector('[data-testid="cluster-join-removed"]'); expect(d).not.toBeNull(); return d as HTMLDetailsElement; });
    expect(det.open).toBe(false);
    expect(det.querySelector('summary')?.textContent).toBe('1 removed');
    expect(det.textContent).toContain('Removed by oidc:bob@acme.com on 2026-11-21.');
    const tip = target.querySelectorAll('[data-testid="source-cluster"]')[1].querySelector('[role="note"]');
    expect(tip?.textContent).toBe('Every data source that reads this cluster answered. Approved by oidc:alice@acme.com on 2026-11-20.'
      + ' This member has no live channel to this hub, so signed-in people are not reviewed there.');
    done();
  });

  it('says the review failed instead of a denial, and when a pending snapshot is stale', async () => {
    const { target, done } = await render(one, joinList({ canApprove: false, hidden: 2, completeness: 'partial',
      limitations: [{ code: 'SOURCE_UNAVAILABLE', source: 'cluster-join-approver', message: 'whether you may approve clusterjoins could not be checked' }] }));
    const counts = await vi.waitFor(() => { const c = target.querySelector('[data-testid="cluster-join-counts"]'); expect(c).not.toBeNull(); return c as HTMLElement; });
    expect(counts.textContent).toBe('2 clusters hidden: whether you may approve them could not be checked.');
    done();
    const stale = await render(one, joinList({ clusters: [{ uid: '9f1c4e2a', name: 'ap-dev', state: 'pending', keyID: KEY, status: 'stale', snapshotAsOf: '2026-11-17T10:00:00Z' }] }));
    const words = await vi.waitFor(() => { const w = stale.target.querySelector('[data-testid="cluster-join-snapshot"]'); expect(w).not.toBeNull(); return w as HTMLElement; });
    expect(words.textContent).toBe('snapshot stale, from 2026-11-17 10:00');
    stale.done();
  });

  it('drops the rows of an earlier read when a refresh fails', async () => {
    entitiesFn.mockResolvedValue(resp(one));
    clustersFn.mockResolvedValueOnce(joinList()).mockRejectedValue(new Error('502'));
    const target = document.createElement('div');
    document.body.appendChild(target);
    const props = $state({ refreshTick: 0 });
    const c = mount(FleetSourcesView, { target, props });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="cluster-join-row"]')).not.toBeNull());
    props.refreshTick = 1;
    flushSync();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="cluster-join-error"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="cluster-join-row"]')).toBeNull();
    unmount(c);
    target.remove();
  });

  it('renders as before on a host with no join reader', async () => {
    const { target, done } = await render(one, null);
    await vi.waitFor(() => expect(clustersFn).toHaveBeenCalled());
    await Promise.resolve();
    flushSync();
    expect(target.querySelector('[data-testid="source-clusters"]')).toBeNull();
    done();
  });

  it('shows an error inside the block, never an empty list', async () => {
    const { target, done } = await render(one, new Error('cannot read ghcr.io/acme/pacto-targets'));
    const err = await vi.waitFor(() => { const e = target.querySelector('[data-testid="cluster-join-error"]'); expect(e).not.toBeNull(); return e as HTMLElement; });
    expect(err.textContent).toBe('cannot read ghcr.io/acme/pacto-targets');
    expect(err.closest('[data-testid="source-clusters"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="cluster-join-pending"]')).toBeNull();
    done();
  });

  // 514-S4: the member's channel and identity model, a muted run after the status word.
  it('shows the link run muted after the word, with the issuer in the tip only', async () => {
    const iss = 'https://login.example/realms/eng';
    const { target, done } = await render([
      { id: 'uid-eu', name: 'eu-prod', status: 'available', targets: 2, link: { channel: 'live', since: '2026-11-20T08:15:00Z', issuers: [{ issuer: iss, model: 'shared-issuer' }] } },
      { id: 'uid-us', name: 'us-west', status: 'available', targets: 1 },
      { id: 'uid-hub', name: 'hub', status: 'available', targets: 3 },
    ], joinList({ clusters: [{ uid: 'uid-us', name: 'us-west', state: 'approved', keyID: KEY, approver: 'oidc:alice@acme.com' }] }));
    const chips = await vi.waitFor(() => {
      const cs = Array.from(target.querySelectorAll('[data-testid="source-cluster"]'));
      expect(cs[1].querySelector('.sc-link')).not.toBeNull();
      return cs;
    });
    const runs = chips.map((c) => c.querySelector<HTMLElement>('.sc-link')?.textContent ?? null);
    expect(runs).toEqual(['live · shared issuer', 'no live channel', null]);
    expect(chips[0].querySelector('.sc-word')?.nextElementSibling?.classList.contains('sc-link')).toBe(true);
    expect(chips[0].querySelector('.sc-link')?.textContent).not.toContain(iss);
    expect(chips[0].querySelector('[role="note"]')?.textContent).toContain(`${iss}: its tokens are accepted.`);
    expect(chips[0].querySelector('[role="note"]')?.textContent).toContain('since 2026-11-20 08:15');
    done();
  });

  it('draws the run in the muted token', () => {
    expect(readFileSync('src/views/FleetSourcesView.svelte', 'utf8')).toMatch(/\.sc-link \{[^}]*color: var\(--c-text-3\)/);
  });

  // 514-S5: the candidate clusters, one collapsed disclosure inside the same block.
  const UID = '9f1c4e2a-0000-4000-8000-000000000001';
  const candidates = [
    { source: 'argocd', kind: 'Secret', namespace: 'argocd', name: 'eu-prod', labels: { env: 'prod', region: 'eu', tier: 'gold', zone: 'a' }, uid: UID,
      uidSource: 'annotation pacto.trianalab.io/cluster-uid', match: 'matched', member: { uid: 'uid-eu', name: 'eu-prod' } },
    { source: 'argocd', kind: 'Secret', namespace: 'argocd', name: 'ap-dev', match: 'same-uid', uid: UID, reason: 'the cluster reports this UID about itself; a person has not approved it under this key' },
    { source: 'capi', kind: 'Cluster', namespace: 'fleet', name: 'us-lab', match: 'unmatched', reason: 'the candidate states no cluster UID' },
  ];
  const withMember = [...one, { id: 'uid-eu', name: 'eu-prod', status: 'available', targets: 3 }];
  async function openCandidates(clusters: unknown[], over: Record<string, unknown> = {}) {
    const r = await render(clusters, joinList({ clusters: [], candidates, candidatesAsOf: '2026-11-23T10:00:00Z', candidatesCompleteness: 'complete', ...over }));
    const det = await vi.waitFor(() => { const d = r.target.querySelector('[data-testid="cluster-candidates"]'); expect(d).not.toBeNull(); return d as HTMLDetailsElement; });
    return { ...r, det, cards: Array.from(det.querySelectorAll<HTMLElement>('[data-testid="cluster-candidate"]')) };
  }

  it('candidates alone show the Clusters block, collapsed', async () => {
    const { det, target, done } = await openCandidates(one);
    expect(det.closest('[data-testid="source-clusters"]')).not.toBeNull();
    expect(det.open).toBe(false);
    expect(det.querySelector('summary')?.textContent).toBe('3 candidate clusters');
    expect(target.querySelectorAll('[data-testid="cluster-candidates"]')).toHaveLength(1);
    done();
  });

  it('groups candidates by source', async () => {
    const { det, done } = await openCandidates(one);
    const groups = Array.from(det.querySelectorAll<HTMLElement>('[data-testid="cluster-candidate-group"]'));
    expect(groups.map((g) => [g.dataset.source, g.querySelectorAll('[data-testid="cluster-candidate"]').length])).toEqual([['argocd', 2], ['capi', 1]]);
    done();
  });

  it('never says not joined', async () => {
    const { det, cards, done } = await openCandidates(one);
    expect(cards.map((c) => c.querySelector('.scc-word')?.textContent)).toEqual(['matched to', 'states the same UID', 'not matched']);
    expect(det.textContent).not.toMatch(/not joined|not a member/);
    done();
  });

  it('matched card links to the member row', async () => {
    const { cards, target, done } = await openCandidates(withMember);
    const row = target.querySelector<HTMLElement>('#sc-member-uid-eu')!;
    row.scrollIntoView = vi.fn();
    cards[0].querySelector<HTMLButtonElement>('[data-testid="cluster-candidate-member"]')!.click();
    expect(row.scrollIntoView).toHaveBeenCalled();
    expect(document.activeElement).toBe(row);
    done();
  });

  it('same-uid card has no link, nor a matched one whose member row is not shown', async () => {
    const { cards, done } = await openCandidates(one);
    expect(cards.map((c) => !!c.querySelector('button.scc-open'))).toEqual([false, false, false]);
    done();
    const shown = await openCandidates(withMember);
    expect(shown.cards.map((c) => !!c.querySelector('button.scc-open'))).toEqual([true, false, false]);
    shown.done();
  });

  it('candidate card has one source mark', async () => {
    const { cards, done } = await openCandidates(one);
    expect(cards.map((c) => c.querySelectorAll('.prov-mark').length)).toEqual([1, 1, 1]);
    expect(cards[2].querySelector('[role="note"]')?.textContent).toBe('From Cluster API Cluster fleet/us-lab. The candidate states no cluster UID.');
    done();
  });

  it('no uid in a visible label', async () => {
    const { cards, done } = await openCandidates(one);
    for (const c of cards) {
      const visible = Array.from(c.children).filter((e) => !e.classList.contains('prov-mark')).map((e) => `${e.textContent} ${e.getAttribute('title') ?? ''}`).join(' ');
      expect(visible).not.toContain(UID.slice(0, 8));
    }
    expect(cards[0].querySelector('[role="note"]')?.textContent).toContain(UID);
    done();
  });

  it('labels collapse after three with the rest in the title', async () => {
    const { cards, done } = await openCandidates(one);
    const l = cards[0].querySelector('.scc-labels')!;
    expect(l.textContent).toBe('env=prod region=eu tier=gold');
    // The count of the rest is its own span, so a long label's ellipsis never clips it.
    expect(cards[0].querySelector('.scc-more')?.textContent).toBe('+1');
    expect(l.getAttribute('title')).toBe('env=prod, region=eu, tier=gold, zone=a');
    expect(cards[0].querySelector('bdi')?.getAttribute('title')).toBe('eu-prod');
    done();
  });

  it('download only for argocd and flux groups', async () => {
    const { det, done } = await openCandidates(one, { candidates: [...candidates, { source: 'flux', kind: 'Secret', namespace: 'flux-system', name: 'us', match: 'unmatched' }] });
    const actions = Array.from(det.querySelectorAll<HTMLElement>('[data-testid="cluster-candidate-group"]')).map((g) => [g.dataset.source, g.querySelector('[data-testid="cluster-candidate-install"]')?.textContent ?? null]);
    expect(actions).toEqual([['argocd', 'Get ApplicationSet'], ['flux', 'Get Flux manifests'], ['capi', null]]);
    done();
  });

  it('download fetches through the client and saves a blob', async () => {
    installFn.mockReset(); saveFn.mockReset();
    installFn.mockResolvedValue('registry: ghcr.io/acme\n');
    const { det, done } = await openCandidates(one);
    det.querySelector<HTMLButtonElement>('[data-testid="cluster-candidate-install"]')!.click();
    await vi.waitFor(() => expect(saveFn).toHaveBeenCalledWith('pacto-members-argocd.yaml', 'registry: ghcr.io/acme\n'));
    expect(installFn).toHaveBeenCalledWith('argocd');
    expect(det.querySelector('[data-testid="cluster-candidate-error"]')).toBeNull();
    done();
  });

  it('download shows the 403 message', async () => {
    const { ApiError } = await import('../lib/api.ts');
    installFn.mockReset(); saveFn.mockReset();
    installFn.mockRejectedValue(new ApiError(403, 'forbidden'));
    const { det, done } = await openCandidates(one);
    det.querySelector<HTMLButtonElement>('[data-testid="cluster-candidate-install"]')!.click();
    const err = await vi.waitFor(() => { const e = det.querySelector('[data-testid="cluster-candidate-error"]'); expect(e).not.toBeNull(); return e as HTMLElement; });
    expect(err.textContent).toBe('Only those who may approve clusterjoins can download the member install.');
    installFn.mockRejectedValue(new ApiError(422, 'the hub advertises no chart version'));
    det.querySelector<HTMLButtonElement>('[data-testid="cluster-candidate-install"]')!.click();
    const errText = () => det.querySelector('[data-testid="cluster-candidate-error"]')?.textContent;
    await vi.waitFor(() => expect(errText()).toBe('the hub advertises no chart version'));
    installFn.mockRejectedValue({});
    det.querySelector<HTMLButtonElement>('[data-testid="cluster-candidate-install"]')!.click();
    await vi.waitFor(() => expect(errText()).toBe('Pacto could not generate the member install.'));
    expect(saveFn).not.toHaveBeenCalled();
    // The error sits right under the button that failed, inside its group.
    expect(det.querySelector('[data-testid="cluster-candidate-group"][data-source="argocd"] .scc-head + [data-testid="cluster-candidate-error"]')).not.toBeNull();
    done();
  });

  it('download is busy until its answer, so a second tap sends nothing', async () => {
    installFn.mockReset(); saveFn.mockReset();
    let answer: (v: string) => void = () => {};
    installFn.mockReturnValue(new Promise<string>((r) => { answer = r; }));
    const { det, done } = await openCandidates(one);
    const btn = det.querySelector<HTMLButtonElement>('[data-testid="cluster-candidate-install"]')!;
    btn.click();
    await vi.waitFor(() => expect(btn.getAttribute('aria-busy')).toBe('true'));
    expect(btn.disabled).toBe(true);
    btn.click();
    answer('registry: ghcr.io/acme\n');
    await vi.waitFor(() => expect(btn.disabled).toBe(false));
    expect(installFn).toHaveBeenCalledTimes(1);
    done();
  });

  it('a matched card names its member in its own truncated span', async () => {
    const long = `production-sa-east-1-${'x'.repeat(42)}`;
    const { cards, done } = await openCandidates(one, { candidates: [{ ...candidates[0], member: { uid: UID, name: long } }] });
    const m = cards[0].querySelector('.scc-word + .scc-member')!;
    expect(m.tagName).toBe('BDI');
    expect(m.getAttribute('title')).toBe(long);
    expect(m.textContent).toBe(long);
    expect(cards[0].querySelector('.scc-word')?.textContent).toBe('matched to');
    done();
  });

  it('hidden candidates counted, and they alone show the block', async () => {
    const { target, done } = await render(one, joinList({ clusters: [], canApprove: false, hiddenCandidates: 2 }));
    const counts = await vi.waitFor(() => { const c = target.querySelector('[data-testid="cluster-join-counts"]'); expect(c).not.toBeNull(); return c as HTMLElement; });
    expect(counts.textContent).toBe('2 candidate clusters hidden: they show to those who may approve clusterjoins.');
    expect(target.querySelector('[data-testid="cluster-candidates"]')).toBeNull();
    done();
  });

  it('says a partial candidate list is partial', async () => {
    const { det, done } = await openCandidates(one, { candidatesCompleteness: 'partial' });
    expect(det.querySelector('summary')?.textContent).toBe('3 candidate clusters, partial');
    done();
  });
});
