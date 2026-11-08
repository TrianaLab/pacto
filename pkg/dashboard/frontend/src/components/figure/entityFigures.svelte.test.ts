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
    revision: { version: '2.0.0', previous: ref('revision', 'svc@1', 'svc 1.9.0'), next: null },
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
    const { target, c } = render(ServiceRevisionsFigure, { revisions: { total: 214, count: 7, items }, currentKey: 'svc@1', self: 'svc', allHref: '#/fleet/revisions?service=svc' });
    await settle();
    expect(target.querySelectorAll('.slab')).toHaveLength(6);
    expect(target.querySelectorAll('.slab.t-accent')).toHaveLength(1);
    const plinth = target.querySelector('[data-a="lplinth"] a') as HTMLAnchorElement;
    expect(plinth.textContent).toBe('209 earlier revisions');
    expect(target.querySelector('.fig-aside a')?.textContent).toBe('214');
    expect(target.textContent).not.toContain('Oldest');
    expect(marks(target, 'revisions')).toBeLessThanOrEqual(20);
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
    expect(target.querySelectorAll('.ofig-prov li')).toHaveLength(2);
    expect(target.querySelector('.ofig-prov a[href*="offering=postgres"]')?.textContent).toBe('24 more providers');
    expect(target.querySelectorAll('.ph.station')).toHaveLength(6);
    expect(target.querySelectorAll('.ph-chips .chip')).toHaveLength(3);
    expect(target.querySelector('.ph-chips [data-nofilter]')?.textContent).toBe('1 more failed consumer');
    expect(target.querySelector('.ph-unknown .nd.unk')).toBeTruthy();
    expect(target.querySelector('.hub.t-accent')).toBeTruthy();
    expect(target.textContent).toContain('2 consumers, not counted and never named');
    expect(target.textContent).toContain('6 in the accepted range, 1 outside it');
    expect(marks(target, 'offering')).toBeLessThanOrEqual(20);
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
