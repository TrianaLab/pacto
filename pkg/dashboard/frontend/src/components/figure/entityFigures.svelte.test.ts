import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, tick } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import RevisionFigures from './RevisionFigures.svelte';
// @ts-expect-error — Svelte component has no declaration file
import ServiceRevisionsFigure from './ServiceRevisionsFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import TargetFigure from './TargetFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import SourceFigure from './SourceFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import OfferingFigure from './OfferingFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import OfferingUseFigure from './OfferingUseFigure.svelte';

// Two renders share figure ids, and jsdom resolves an id selector document-wide, so the
// helpers match the id as an attribute.
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;
const ref = (kind: string, key: string, label = key): Any => ({ kind, key, label, href: `/fleet/${kind}s/${encodeURIComponent(key)}` });

function render(C: Any, props: Any, reduced = false) {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: reduced && q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
  const target = document.createElement('div');
  document.body.appendChild(target);
  return { target, c: mount(C, { target, props }) };
}
const settle = async () => { for (let i = 0; i < 5; i++) await tick(); };
const stage = (t: HTMLElement, id: string) => t.querySelector(`[id="fig-${id}"] .fig-stage`)!.innerHTML;
const marks = (t: HTMLElement, id: string) => t.querySelectorAll(`#fig-${id} .mk`).length;

afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ''; sessionStorage.clear(); localStorage.clear(); });

describe('RevisionFigures', () => {
  const props = {
    stages: [{ name: 'structural', status: 'pass' }, { name: 'policy', status: 'fail', reason: 'no owner' }, { name: 'environments', status: 'skip' }],
    revision: {
      version: '2.0.0', previous: ref('revision', 'svc@1', 'svc 1.9.0'), next: null, service: ref('service', 'svc'),
      position: 1, revisions: 214, current: true, serviceTargets: 11, exactTargets: { total: 6 }, inferredTargets: { total: 2 },
      evidenceSources: { total: 5, count: 3, items: [{ ...ref('source', 'edge'), status: 'unavailable', secondary: 'kubernetes' }, { ...ref('source', 'files'), status: 'stale' }, { ...ref('source', 'oci'), status: 'available' }] },
      evidenceSourceCounts: { total: 5, available: 2, stale: 1, unavailable: 1 },
    },
    self: 'svc@2', service: 'svc', historyHref: '#/fleet/revisions?service=svc',
  };
  it('walks the validation rail to the same final frame under reduced motion (G2)', async () => {
    const a = render(RevisionFigures, props);
    const b = render(RevisionFigures, props, true);
    await settle();
    expect(stage(b.target, 'validation')).toBe(stage(a.target, 'validation'));
    expect(b.target.textContent).toContain('Step 3 of 3');
    expect(a.target.querySelector('#fig-validation .fig-q')?.textContent).toBe('Where did validation stop?');
    expect(a.target.querySelector('#fig-validation figcaption')?.textContent).toContain('Validation stopped at Policy');
    unmount(a.c); unmount(b.c);
  });
  it('draws the revision between its neighbours, with the service name dropped', async () => {
    const { target, c } = render(RevisionFigures, props);
    await settle();
    expect([...target.querySelectorAll('#fig-around .stack-row')].map((r) => r.textContent?.replace(/\s+/g, ' ').trim())).toEqual(['2.0.0 this revision', '1.9.0 previous']);
    expect(target.querySelectorAll('#fig-around .slab.dash')).toHaveLength(1);
    expect(target.querySelector('figure svg text')).toBeNull();
    unmount(c);
  });
  it('says where the revision stands and how many of the service\'s targets run it (r3)', async () => {
    const { target, c } = render(RevisionFigures, props);
    await settle();
    const aside = target.querySelector('#fig-around .fig-aside')!.textContent!.replace(/\s+/g, ' ');
    expect(aside).toContain('Current: 1 of 214 revisions, newest first');
    expect(aside).toContain("8 of the service's 11 run this revision");
    expect((target.querySelector('#fig-around .fig-aside a[href*="targets"]') as HTMLAnchorElement).textContent).toBe('11');
    unmount(c);
  });
  it('wires each source into the revision in its state\'s style and counts the rest', async () => {
    const { target, c } = render(RevisionFigures, props);
    await settle();
    expect(target.querySelector('#fig-evidence .fig-q')?.textContent).toBe('Where does the evidence about it come from?');
    expect([...target.querySelectorAll('#fig-evidence .evfig-s .nd')].map((n) => n.getAttribute('class')?.match(/t-\w+/)?.[0])).toEqual(['t-err', 't-warn', 't-ok']);
    expect(target.querySelector('#fig-evidence .evfig-s a')?.getAttribute('href')).toBe('#/fleet/sources/edge');
    expect(target.querySelector('#fig-evidence .cnt')?.textContent).toBe('+2');
    // One of the two unnamed sources is current, the other is not: their wire is not solid.
    expect(target.textContent).toContain('not all current');
    expect(target.querySelector('#fig-evidence .hub.t-accent')).toBeTruthy();
    expect(target.querySelector('#fig-evidence figcaption')?.textContent).toContain('1 source is unavailable');
    unmount(c);
  });
  it('draws no evidence figure when no source reports it', async () => {
    const { target, c } = render(RevisionFigures, { ...props, revision: { ...props.revision, evidenceSources: { total: 0 }, revisions: 0 } });
    await settle();
    expect(target.querySelector('#fig-evidence')).toBeNull();
    expect(target.textContent).not.toContain('Position');
    unmount(c);
  });
  it('draws no rail for a revision never validated', async () => {
    const { target, c } = render(RevisionFigures, { ...props, stages: [] });
    await settle();
    expect(target.querySelector('#fig-validation')).toBeNull();
    unmount(c);
  });
});

describe('ServiceRevisionsFigure', () => {
  it('stacks five layers and folds the rest into a plinth that links to the history', async () => {
    const items = Array.from({ length: 7 }, (_, i) => ref('revision', `svc@${i}`, `svc 1.0.${9 - i}`));
    const { target, c } = render(ServiceRevisionsFigure, { revisions: { total: 214, count: 7, items }, current: items[1], self: 'svc', allHref: '#/fleet/revisions?service=svc' });
    await settle();
    expect(target.querySelectorAll('.slab')).toHaveLength(6);
    expect(target.querySelectorAll('.slab.t-accent')).toHaveLength(1);
    const plinth = target.querySelector('[data-a="lplinth"] a') as HTMLAnchorElement;
    expect(plinth.textContent).toBe('209 earlier revisions');
    expect(target.querySelector('.fig-aside a')?.textContent).toBe('214');
    expect(target.textContent).not.toContain('Oldest');
    expect(marks(target, 'revisions')).toBeLessThanOrEqual(20);
    expect(target.querySelector('.fig-aside')?.textContent).toContain('Current1.0.8');
    unmount(c);
  });
  it('names the revision in use when it is folded into the plinth', async () => {
    const items = Array.from({ length: 7 }, (_, i) => ref('revision', `svc@${i}`, `svc 1.0.${9 - i}`));
    const { target, c } = render(ServiceRevisionsFigure, { revisions: { total: 214, count: 7, items }, current: items[6], self: 'svc', allHref: '#' });
    await settle();
    expect(target.querySelectorAll('.slab.t-accent')).toHaveLength(0);
    expect(target.querySelector('[data-a="lplinth"]')?.textContent).toContain('the current 1.0.3 is among them');
    expect(target.querySelector('.fig-aside a[href="#/fleet/revisions/svc%406"]')?.textContent).toBe('1.0.3');
    unmount(c);
  });
});

describe('TargetFigure', () => {
  it('wires the plinth to the revision it runs and stamps its compliance', async () => {
    const { target, c } = render(TargetFigure, { target: { linkState: 'inferred', revision: ref('revision', 'svc@1', 'svc 1.0.0') }, status: 'NonCompliant', revisionLabel: '1.0.0' });
    await settle();
    expect(target.querySelector('.plinth .slab.t-accent')).toBeTruthy();
    expect(target.querySelector('.stamp.t-err')?.textContent).toBe('Not compliant');
    expect(target.querySelector('[data-a="rev"] a')?.textContent).toBe('1.0.0');
    unmount(c);
  });
  it('names no revision on an ambiguous link', async () => {
    const { target, c } = render(TargetFigure, { target: { linkState: 'ambiguous', revision: ref('revision', 'svc@1') }, status: 'Unknown', revisionLabel: '' });
    await settle();
    expect(target.querySelector('[data-a="rev"] a')).toBeNull();
    expect(target.textContent).toContain('No revision named');
    expect(target.querySelector('.stamp.t-neutral')).toBeTruthy();
    unmount(c);
  });
});

describe('SourceFigure', () => {
  it('says an unavailable source reports nothing in this snapshot', async () => {
    const { target, c } = render(SourceFigure, { source: { health: 'unavailable', kind: 'Kubernetes cluster' }, name: 'edge', asOf: 'today' });
    await settle();
    expect(target.querySelector('.nd.t-err')).toBeTruthy();
    expect(target.querySelector('.hub.t-accent')).toBeTruthy();
    expect(target.textContent).toContain('Nothing in this snapshot');
    // That is no report time, so it is never labelled as one.
    expect(target.querySelector('.fig-aside dt')?.textContent).toBe('Reports');
    unmount(c);
  });
  it('counts what a current source reports', async () => {
    const { target, c } = render(SourceFigure, { source: { health: 'available', revisionCount: 1, targetCount: 3 }, name: 'oci' });
    await settle();
    expect(target.textContent).toContain('1 contract revision and 3 operational targets');
    unmount(c);
  });
});

describe('OfferingFigure', () => {
  const provider = (k: string) => ({ service: ref('service', k) });
  it('draws two provider marks, the phase track from the tally and the failed consumers the backend named', async () => {
    const { target, c } = render(OfferingFigure, {
      offering: 'postgres', hidden: 2, providersHref: '#/fleet/services?offering=postgres',
      providers: { total: 25, count: 3, items: [provider('a'), provider('b'), provider('c')] },
      consumers: { total: 9, range: { satisfied: 6, unsatisfied: 1, unknown: 1, none: 1 }, phases: { requested: 1, provisioning: 1, ready: 5, failed: 4, unknown: 2 },
        failed: { total: 4, count: 3, items: [ref('service', 'x'), ref('service', 'y'), ref('service', 'z')] } },
    });
    await settle();
    // Not reported takes a mark, so the providers fold into one count (G1: 20 marks).
    expect(target.querySelectorAll('.ofig-prov li')).toHaveLength(1);
    expect(target.querySelector('.ofig-prov a[href*="offering=postgres"]')?.textContent).toBe('25 providers');
    expect(target.querySelectorAll('.ph.station')).toHaveLength(6);
    expect(target.querySelectorAll('.ph-chips .chip')).toHaveLength(3);
    // Every phase count and "N more" lists the consumers behind it (T3).
    const more = target.querySelector('.ph-chips > a:not(.chip)') as HTMLAnchorElement;
    expect(more.textContent).toBe('1 more');
    expect(more.getAttribute('aria-label')).toBe('1 more failed consumer');
    expect(more.getAttribute('href')).toBe('#/fleet/services?consumes=postgres&phase=failed');
    expect(target.querySelector('.ph-ready a')?.getAttribute('href')).toBe('#/fleet/services?consumes=postgres&phase=ready');
    expect(target.querySelector('.ph-unknown a')?.getAttribute('href')).toBe('#/fleet/services?consumes=postgres&phase=unknown');
    expect(target.querySelector('.ph-deleting [data-nofilter]')?.textContent).toBe('0 · 0%');
    // Hidden consumers are counted, never named: the only names are the ones the backend gave.
    expect([...target.querySelectorAll('#fig-offering a.chip')].map((a) => a.textContent)).toEqual(['x', 'y', 'z']);
    expect(target.querySelector('.ph-unknown .nd.unk')).toBeTruthy();
    expect(target.querySelector('.hub.t-accent')).toBeTruthy();
    expect(target.textContent).toContain('2 consumers, not counted and never named');
    expect(target.textContent).toContain('6 in the accepted range, 1 outside it');
    expect(marks(target, 'offering')).toBeLessThanOrEqual(20);
    unmount(c);
  });
  it('names two providers when every request has a reported phase', async () => {
    const { target, c } = render(OfferingFigure, { offering: 'q', hidden: 0, providersHref: '#', providers: { total: 25, items: [provider('a'), provider('b')] }, consumers: { total: 1, phases: { ready: 1 } } });
    await settle();
    expect([...target.querySelectorAll('.ofig-prov li')].map((l) => l.textContent?.trim())).toEqual(['a', '+2424 more providers']);
    unmount(c);
  });
  it('keeps every phase at 0 and says nobody asked when there are no requests', async () => {
    const { target, c } = render(OfferingFigure, { offering: 'q', hidden: 0, providersHref: '#', providers: { total: 1, items: [provider('a')] }, consumers: { total: 0 } });
    await settle();
    expect(target.querySelectorAll('.ph.station')).toHaveLength(5);
    expect(target.textContent).toContain('No consumer has asked a provider for a resource yet.');
    expect(target.querySelector('.ph-chips')).toBeNull();
    unmount(c);
  });
});

describe('OfferingUseFigure (A6)', () => {
  it('reveals only the current station, with its reported time, and leaves earlier ones at rest', async () => {
    const props = { consumer: 'orders', useKey: 'orders:pg:dependency:0', request: { phase: 'Failed', environment: 'prod', asOf: '2026-11-05T23:13:00Z', verified: false } };
    const a = render(OfferingUseFigure, props);
    const b = render(OfferingUseFigure, props, true);
    await settle();
    expect(a.target.querySelector('.fig-q')?.textContent).toBe('Is the request of orders in prod ready?');
    expect([...a.target.querySelectorAll('.rl-st')].map((s) => s.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
      'Requested came before', 'Provisioning came before', expect.stringMatching(/^Failed last reported at Nov [56], 2026/),
    ]);
    expect(a.target.querySelector('figcaption')?.textContent).toContain('The report is not signed.');
    // One step: the reduced-motion step-through counts it.
    expect(b.target.textContent).toContain('Step 1 of 1');
    expect(stage(b.target, 'use')).toBe(stage(a.target, 'use'));
    unmount(a.c); unmount(b.c);
  });
  it('draws a request with no trusted phase hatched, and tells no story', async () => {
    const { target, c } = render(OfferingUseFigure, { consumer: 'orders', useKey: 'k', request: { phase: 'Unknown' } }, true);
    await settle();
    expect(target.querySelectorAll('.nd.unk')).toHaveLength(3);
    expect(target.textContent).not.toContain('Step 1');
    expect(target.querySelector('figcaption')?.textContent).toContain('readiness is unknown');
    unmount(c);
  });
});
