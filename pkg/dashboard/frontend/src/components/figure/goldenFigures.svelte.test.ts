import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, tick } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import OriginFigure from './OriginFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import GoldenPathFigure from './GoldenPathFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import CreationFigure from './CreationFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import CreationExplainer from './explain/CreationExplainer.svelte';

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
const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
afterEach(() => { vi.unstubAllGlobals(); entitiesFn.mockReset(); document.body.innerHTML = ''; sessionStorage.clear(); localStorage.clear(); });

describe('OriginFigure', () => {
  it('flows golden path lanes into a neutral fleet hub, counts hidden services and names none', async () => {
    const t = render(OriginFigure, {
      aggregate: { services: 10, origins: { byPath: [{ key: 'acme/http', label: 'http', services: 6, nonCompliant: 2, hidden: 1 }], paths: 3, beyond: { services: 1 }, none: { services: 3, nonCompliant: 0 }, hidden: 1 } },
      hrefs: { services: (b: string) => `#/s?cf=${b}`, paths: '#/gp' },
    });
    await settle();
    expect(t.querySelector('.fig-q')?.textContent).toBe('Which golden paths created the services in the fleet?');
    expect(t.querySelector('.rank--in .hub.t-neutral')).toBeTruthy();
    expect(Array.from(t.querySelectorAll('.lane-name')).map((n) => n.textContent)).toEqual(['http', 'Other paths', 'None declared']);
    expect(Array.from(t.querySelectorAll('.lane-more')).map((a) => a.getAttribute('href'))).toEqual(['#/s?cf=acme/http', '#/s?cf=(none)']);
    const tile = t.querySelector('.count-tile');
    expect(tile?.querySelector('a')?.getAttribute('href')).toBe('#/gp');
    expect(tile?.textContent).toContain('+2 more');
    expect(tile?.textContent).toContain('1 you may not see');
    expect(t.querySelector('figcaption')?.textContent).toContain('1 service created from a golden path is hidden by your access and in no count.');
    expect(t.querySelector('.fig-ctl button[aria-pressed]')?.textContent).toBe('Compact');
  });
});

describe('GoldenPathFigure', () => {
  it('wires the accent template into the posture of the services it created', async () => {
    entitiesFn.mockResolvedValue({ entities: [], total: 3, aggregate: { services: 3, serviceCompliance: { compliant: 1, nonCompliant: 1, unknown: 1 } }, meta: { asOf: '2026-11-08T10:00:00Z' } });
    const t = render(GoldenPathFigure, { path: 'acme/http', name: 'http-service', version: '1.0.0', hidden: 2 });
    await settle();
    expect(entitiesFn).toHaveBeenCalledWith({ kinds: ['service'], template: 'exclude', createdFrom: 'acme/http', limit: 1 });
    expect(t.querySelector('.hub.t-accent')).toBeTruthy();
    expect(t.querySelector('.gpfig-hl')?.textContent).toContain('Template 1.0.0');
    const rows = Array.from(t.querySelectorAll('a.prow-n')).map((a) => a.getAttribute('href') ?? '');
    expect(rows.length).toBe(3);
    expect(rows.every((h) => h.includes('createdFrom=acme%2Fhttp') && h.includes('status='))).toBe(true);
    expect(t.querySelector('.facts')?.textContent).toContain('You may not see2');
    expect(t.querySelector('figcaption')?.textContent).toContain('2 more services created from it are hidden by your access and in no count.');
  });
  it('draws nothing when it created nothing anyone can count', async () => {
    entitiesFn.mockResolvedValue({ entities: [], total: 0, aggregate: { services: 0 }, meta: {} });
    const t = render(GoldenPathFigure, { path: 'acme/http', name: 'http-service' });
    await settle();
    expect(t.querySelector('[data-fig]')).toBeNull();
  });
});

describe('CreationFigure', () => {
  it('draws unknown steps hatched and neutral, never done', async () => {
    const t = render(CreationFigure, { steps: [{ step: 'contract', status: 'done', detail: 'read' }, { step: 'deployed', status: 'unknown', detail: 'no target' }] });
    await settle();
    const nodes = Array.from(t.querySelectorAll('.rail .nd'));
    expect(nodes[0].classList.contains('t-ok')).toBe(true);
    expect(nodes[1].classList.contains('t-neutral') && nodes[1].classList.contains('unk')).toBe(true);
    expect(t.querySelector('figcaption')?.textContent).toContain('2 steps: 1 done, 1 unknown, 0 failed.');
  });
});

describe('CreationExplainer', () => {
  it('explains the mechanism with Replay and no status tone', async () => {
    const t = render(CreationExplainer, { requires: ['postgres-database'] });
    await settle();
    expect(t.querySelector('.fig--explain')).toBeTruthy();
    expect(t.textContent).toContain('It also requests postgres-database');
    expect(t.querySelectorAll('.t-ok, .t-warn, .t-err').length).toBe(0);
  });
});
