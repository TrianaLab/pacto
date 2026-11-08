import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, tick } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import RankFigure from './RankFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import TrustFigure from './TrustFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import PostureFigure from './PostureFigure.svelte';
import { complianceSegments } from '../../lib/distributions.ts';

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
    expect(t.querySelector('figcaption')?.textContent).toContain('The highest rate is a: 30% of 20 services.');
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
