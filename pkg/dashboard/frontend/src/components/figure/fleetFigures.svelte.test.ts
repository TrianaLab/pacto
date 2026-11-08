import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, tick } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import RankFigure from './RankFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import TrustFigure from './TrustFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import PostureFigure from './PostureFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import KnowsExplainer from './explain/KnowsExplainer.svelte';
// @ts-expect-error — Svelte component has no declaration file
import EdgeExplainer from './explain/EdgeExplainer.svelte';
// @ts-expect-error — Svelte component has no declaration file
import ScopedPostureFigure from './ScopedPostureFigure.svelte';
import { complianceSegments } from '../../lib/distributions.ts';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;
function render(C: Any, props: Any) {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
  const target = document.createElement('div');
  document.body.appendChild(target);
  mount(C, { target, props });
  return target;
}
const settle = async () => { for (let i = 0; i < 5; i++) await tick(); };
afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ''; sessionStorage.clear(); localStorage.clear(); });

describe('RankFigure', () => {
  it('fans a neutral hub into lanes whose counts list what they count', async () => {
    const t = render(RankFigure, {
      kind: 'owner',
      aggregate: { services: 50, serviceCompliance: { nonCompliant: 9 }, nonCompliantByOwner: [{ key: 'team:a', label: 'a', services: 20, nonCompliant: 6 }], nonCompliantOwners: 3, nonCompliantBeyondRanking: 2, nonCompliantUnidentified: 1 },
      hrefs: { services: (o: string) => `#/s?o=${o}`, groups: '#/owners?sort=nonCompliant' },
    });
    await settle();
    expect(t.querySelector('.fig-q')?.textContent).toBe('Largest owners by non-compliant services');
    expect(t.querySelector('.hub.t-neutral')).toBeTruthy();
    expect(Array.from(t.querySelectorAll('.lane-name')).map((n) => n.textContent)).toEqual(['a', 'Other owners', 'No identified owner']);
    // The group node is neutral; its bar carries the err tone.
    expect(t.querySelector('.lane .nd')?.classList.contains('t-neutral')).toBe(true);
    expect(t.querySelector('.lane .mbar i')?.classList.contains('t-err')).toBe(true);
    expect(Array.from(t.querySelectorAll('.lane-more')).map((a) => a.getAttribute('href'))).toEqual(['#/s?o=team:a', '#/s?o=']);
    expect(t.querySelector('a.lane-note')?.getAttribute('href')).toBe('#/owners?sort=nonCompliant');
    expect(t.querySelector('figcaption')?.textContent).toContain('The highest rate is a: 30% of 20 services; sort the table by Rate to see the rest.');
  });
});

describe('TrustFigure', () => {
  it('counts sources by state on the overview, each count a filter', async () => {
    const t = render(TrustFigure, { mode: 'overview', counts: { total: 5, available: 3, stale: 1, unavailable: 1 }, asOf: 'Nov 8', stateHref: (s: string) => `#/src?h=${s}` });
    await settle();
    expect(Array.from(t.querySelectorAll('.src-n')).map((a) => [a.textContent, a.getAttribute('href')]))
      .toEqual([['1', '#/src?h=unavailable'], ['1', '#/src?h=stale'], ['3', '#/src?h=available']]);
    expect(t.querySelectorAll('.bus i')).toHaveLength(3);
    expect(t.querySelector('.trust-hl .badge')?.textContent).toBe('Partial');
  });
  it('names the worst sources on the Sources page and tallies the rest', async () => {
    const worst = [{ key: 'a', label: 'edge-cluster', status: 'unavailable', href: '/fleet/sources/a' }];
    const t = render(TrustFigure, { mode: 'sources', counts: { total: 30, available: 29, unavailable: 1 }, worst, stateHref: (s: string) => `#/src?h=${s}` });
    await settle();
    expect(t.querySelector('.src .id')?.getAttribute('href')).toBe('#/fleet/sources/a');
    expect(Array.from(t.querySelectorAll('.src-t > a:not(.id)')).map((a) => a.textContent)).toEqual(['29 more current']);
    expect(t.querySelector('.fig-ctl button')?.textContent).toBe('Compact');
  });
  it('tallies current sources per kind, each linking to that kind, and never links the kinds folded together', async () => {
    const worst = [{ key: 'a', label: 'edge-cluster', status: 'unavailable', href: '/fleet/sources/a' }];
    const t = render(TrustFigure, {
      mode: 'sources', worst, stateHref: (s: string, k?: string) => `#/src?h=${s}&k=${k ?? ''}`,
      counts: { total: 60, available: 59, unavailable: 1, availableByKind: [{ kind: 'kubernetes', count: 41 }, { kind: 'oci', count: 17 }, { kind: 'local', count: 1 }] },
    });
    await settle();
    expect(Array.from(t.querySelectorAll('.src-t > a:not(.id)')).map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['41 more Kubernetes clusters', '#/src?h=available&k=kubernetes'], ['17 more OCI registries', '#/src?h=available&k=oci'], ['1 more local directory', '#/src?h=available&k=local'],
    ]);
    expect(t.querySelector('[data-testid="provenance"]')).toBeNull();
  });
  it('says the snapshot is complete when every source is current', async () => {
    const t = render(TrustFigure, { mode: 'overview', counts: { total: 2, available: 2 }, stateHref: () => '#' });
    await settle();
    expect(t.querySelector('figcaption')?.textContent).toContain('Every source is current');
  });
});

describe('PostureFigure', () => {
  it('keeps a row for every state and draws only the non-empty strata', async () => {
    const t = render(PostureFigure, { question: 'q', segments: complianceSegments({ nonCompliant: 2, compliant: 8 }), total: 10, scope: 'All 10 services' });
    await settle();
    expect(t.querySelectorAll('.prow')).toHaveLength(8);
    expect(t.querySelectorAll('.pc')).toHaveLength(2);
    expect(t.querySelector('.prow [data-nofilter]')).toBeTruthy();
    const empty = render(PostureFigure, { question: 'q', segments: complianceSegments({}), total: 0, scope: 'None' });
    await settle();
    expect(empty.querySelector('figcaption')?.textContent).toContain('None: none.');
  });
});

describe('explainers (How this works)', () => {
  it('draws how Pacto knows as a four-station walk in neutral and accent, with Replay', async () => {
    const t = render(KnowsExplainer, {});
    await settle();
    expect(t.querySelector('.fig--explain')).toBeTruthy();
    expect(Array.from(t.querySelectorAll('.rl-l b')).map((b) => b.textContent)).toEqual(['Sources', 'Snapshot', 'Evidence', 'Verdict']);
    expect(t.querySelector('.fig-ctl button')?.textContent).toBe('Replay');
    expect(t.innerHTML).not.toMatch(/\bt-(ok|warn|err)\b/);
    expect(t.querySelector('figcaption')?.textContent).toContain('It shows no data from your fleet.');
  });
  it('draws four edges into one service, one per wire style, with no data', async () => {
    const t = render(EdgeExplainer, {});
    await settle();
    expect(Array.from(t.querySelectorAll('.evfig-t b')).map((b) => b.textContent)).toEqual(['Observed', 'Projected', 'Stale', 'Missing']);
    expect(t.querySelectorAll('.fig-ctl')).toHaveLength(0);
    expect(t.innerHTML).not.toMatch(/\bt-(ok|warn|err)\b/);
  });
});

describe('ScopedPostureFigure', () => {
  const svc = { meta: { asOf: '2026-11-08T10:00:00Z', sourceCounts: { total: 2, available: 2 } }, total: 9, count: 0, entities: [],
    aggregate: { services: 9, serviceCompliance: { compliant: 6, nonCompliant: 3 }, ownership: { consistent: 6, conflicting: 2, unowned: 1 }, unidentifiedOwnership: 1 } };
  const span = (kind: string, n: number) => ({ total: n, count: 3, entities: [0, 1, 2].map((i) => ({ kind, key: `${kind}-${i}`, label: `${kind}-${i}`, href: `/fleet/${kind}s/${kind}-${i}` })) });
  afterEach(() => entitiesFn.mockReset());

  it('asks how compliant a person\'s services are and names the domains they span, the rest one link away', async () => {
    entitiesFn.mockImplementation((o: { kinds: string[] }) => Promise.resolve(o.kinds[0] === 'service' ? svc : span('domain', 5)));
    const t = render(ScopedPostureFigure, { filter: { ownerKey: 'dri:alice' } });
    await vi.waitFor(() => expect(t.querySelector('[data-testid="posture-span"] .chip')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['domain'], ownerKey: 'dri:alice', limit: 3 }));
    expect(t.querySelector('.fig-q')?.textContent).toBe('How compliant are the services this person owns?');
    const more = t.querySelector('[data-testid="posture-span"] a[data-lists]') as HTMLAnchorElement;
    expect([more.textContent, more.getAttribute('href'), more.dataset.lists]).toEqual(['2 more', '#/fleet/domains?ownerKey=dri%3Aalice', '5']);
    expect(t.querySelector('.fig-aside a')?.getAttribute('href')).toBe('#/fleet/services?ownerKey=dri%3Aalice');
    expect(t.querySelector('.fig-aside')?.textContent).not.toContain('No identified owner');
  });
  it('on a domain page names its owners and counts the services no owner can name', async () => {
    entitiesFn.mockImplementation((o: { kinds: string[] }) => Promise.resolve(o.kinds[0] === 'service' ? svc : span('owner', 3)));
    const t = render(ScopedPostureFigure, { filter: { businessDomain: 'pay' } });
    await vi.waitFor(() => expect(t.querySelector('[data-testid="posture-span"] .chip')).toBeTruthy());
    expect(t.querySelector('.fig-q')?.textContent).toBe('How compliant are the services in this domain?');
    expect(t.querySelector('[data-testid="posture-span"] a[data-lists]')).toBeNull();
    const none = Array.from(t.querySelectorAll('.fig-aside a')).find((a) => a.getAttribute('href')?.includes('unidentified'));
    expect([none?.textContent, none?.getAttribute('href')]).toEqual(['4', '#/fleet/services?ownership=unidentified&businessDomain=pay']);
  });
});
