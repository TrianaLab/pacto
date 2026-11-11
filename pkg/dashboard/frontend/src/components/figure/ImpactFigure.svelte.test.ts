import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ImpactFigure from './ImpactFigure.svelte';
import { GLYPH_PATH } from '../../lib/figure.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;
const consumer = (key: string, o: Any = {}): Any => ({
  service: { kind: 'service', key, label: key, href: `/fleet/services/${key}` },
  path: [], pathTotal: 0, pathTruncated: false, depth: 1, direct: true, confidence: 'contractual', compatibilityVerdict: 'unknown', ...o,
});
const impact = (): Any => ({
  meta: { completeness: 'partial', asOf: '2026-11-07T14:04:00Z', sourceCounts: { total: 50, available: 48, partial: 0, stale: 0, unavailable: 2 } },
  service: { kind: 'service', key: 'payments-service', label: 'payments-service', href: '/fleet/services/payments-service' },
  oldRevision: { kind: 'revision', key: 'p@1', label: 'p 2.0.1', version: '2.0.1', href: '' },
  newRevision: { kind: 'revision', key: 'p@2', label: 'p 3.0.0', version: '3.0.0', href: '' },
  classification: 'BREAKING', gate: 'blocked', gateReason: 'breaking changes affect active consumers', snapshotId: 's', snapshotMatch: true,
  changes: { total: 1, count: 1, breaking: 1, potential: 0, nonBreaking: 0, truncated: false, items: [] },
  consumers: {
    total: 1284, direct: 412, hidden: 3, refused: 5, count: 0, limit: 100, offset: 0, truncated: false, items: [], byVerdict: [], byConfidence: [],
    byOutcome: [{ key: 'incompatible', count: 37 }, { key: 'unknown', count: 52 }, { key: 'compatible', count: 1189 }, { key: 'unclassified', count: 6 }],
    byOwner: [{ owner: 'team-a', consumers: 40, incompatible: 9 }], beyondRanking: 1200, noOwner: 44, owners: 120,
    highlights: [consumer('checkout-web', { compatibilityVerdict: 'incompatible', blocking: true }), consumer('refunds-worker', { compatibilityVerdict: 'incompatible' }), consumer('search-scheduler')],
  },
});

function render(reduced: boolean) {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: reduced && q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ImpactFigure, { target, props: { impact: impact(), autoplay: true } });
  return { target, c };
}

afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ''; });

describe('ImpactFigure', () => {
  it('keeps two owners that share a label apart and names their namespace', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
    const im = impact();
    im.consumers.byOwner = [
      { owner: 'shop', ownerKey: 'dri:shop', consumers: 2, incompatible: 0 },
      { owner: 'shop', ownerKey: 'team:shop', consumers: 1, incompatible: 1 },
      { owner: 'core', ownerKey: 'team:core', consumers: 1, incompatible: 0 },
    ];
    const target = document.createElement('div');
    document.body.appendChild(target);
    const c = mount(ImpactFigure, { target, props: { impact: im } });
    flushSync();
    const rows = [...target.querySelectorAll('[data-testid="impact-owners"] li')].map((li) => li.textContent?.replace(/\s+/g, ' ').trim());
    expect(rows).toEqual(['shop (DRI)2 consumers', 'shop (Team)1 consumer, 1 incompatible', 'core1 consumer']);
    unmount(c);
  });
  it('renders the same drawing as its final frame and under reduced motion (G2)', async () => {
    const a = render(false);
    await vi.waitFor(() => expect(a.target.querySelector('.ring-on')).toBeTruthy());
    const b = render(true);
    await vi.waitFor(() => expect(b.target.querySelector('.ring-on')).toBeTruthy());
    const stage = (t: HTMLElement) => t.querySelector('.fig-stage')!.innerHTML;
    expect(stage(b.target)).toBe(stage(a.target));
    expect(b.target.querySelector('.nar-hid')).toBeNull();
    expect(b.target.textContent).toContain('Step 4 of 4');
    expect(a.target.querySelector('[data-narration]')?.getAttribute('data-narration')).not.toBe('playing');
    // Skip is idle whenever nothing plays, and stays focusable (aria-disabled, not
    // disabled, so pressing it never drops focus to the body); reduced motion steps instead.
    const skip = [...a.target.querySelectorAll('button')].find((x) => x.textContent === 'Skip to final frame') as HTMLButtonElement;
    expect([skip.disabled, skip.getAttribute('aria-disabled')]).toEqual([false, 'true']);
    expect([...b.target.querySelectorAll('button')].map((x) => x.textContent)).toEqual(expect.arrayContaining(['Previous', 'Next']));
    unmount(a.c); unmount(b.c);
  });

  it('steps back through the story under reduced motion, hiding what has not happened yet', async () => {
    const { target, c } = render(true);
    await vi.waitFor(() => expect(target.querySelector('.ring-on')).toBeTruthy());
    ([...target.querySelectorAll('button')].find((x) => x.textContent === 'Previous') as HTMLButtonElement).click();
    flushSync();
    expect(target.textContent).toContain('Step 3 of 4');
    expect(target.querySelector('[data-m="gate"]')?.classList.contains('nar-hid')).toBe(true);
    expect(target.querySelector('.ring-on')).toBeNull();
    unmount(c);
  });

  it('puts words in HTML only, rings one chip and states the answer in the card', async () => {
    const { target, c } = render(false);
    await vi.waitFor(() => expect(target.querySelector('.ring-on')).toBeTruthy());
    expect(target.querySelector('figure svg text')).toBeNull();
    expect(target.querySelectorAll('[data-ring]')).toHaveLength(1);
    expect(target.querySelector('[data-ring]')?.getAttribute('title')).toBe('checkout-web');
    expect(target.querySelectorAll('.lane')).toHaveLength(5);
    expect([...target.querySelectorAll('.lane-name')].map((n) => n.textContent)).toEqual(['Incompatible', 'Unknown', 'Compatible', 'Hidden from you', 'Unclassified']);
    expect(target.querySelectorAll('.opencap')).toHaveLength(4);
    expect(target.querySelector('.lane-sub')?.textContent).toContain('5 of them refuse a required guarantee');
    expect(target.querySelector('[data-testid="impact-gate"]')?.textContent).toContain('Release gate, decided by this lane');
    const card = target.querySelector('[data-testid="changes-answer"]')!.textContent!.replace(/\s+/g, ' ');
    expect(card).toContain('At least 1,284: 412 direct, 872 indirect. Consumers known only to the 2 unavailable sources are not counted.');
    expect(card).toContain('team-a40 consumers, 9 incompatible 1,200 more consumers of other owners; 44 with no identified owner');
    expect(card).toContain('Gate The release gate blocks it: breaking changes affect active consumers.');
    // The refusal note under Incompatible carries the err glyph on its err node.
    expect(target.querySelector('.lane-sub .nd.t-err path.gp')?.getAttribute('d')).toBe(GLYPH_PATH.err);
    expect(card).toContain('3, not counted and never named');
    expect(target.querySelector('figcaption')?.textContent).toContain('If this revision is published, it breaks 37 of at least 1,284 consumers');
    unmount(c);
  });
  it('draws an event answer from one neutral subject node, with no earlier or later layer', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
    const im = impact();
    Object.assign(im, {
      event: { kind: 'unavailable', service: 'payments-service' }, oldRevision: undefined, newRevision: undefined, classification: '', gate: 'unverified',
      gateReason: '1 required consumer runs on prod/eu-1', changedOwner: { kind: 'owner', key: 'team:pay', label: 'pay', href: '/fleet/owners/team%3Apay' },
    });
    im.consumers.byOutcome = [{ key: 'breaks', count: 2 }, { key: 'may-degrade', count: 1 }, { key: 'unknown', count: 0 }];
    im.consumers.total = 3; im.consumers.direct = 2; im.consumers.hidden = 0;
    im.consumers.highlights = [consumer('checkout-web', { effect: 'breaks' })];
    im.consumers.byOwner = [{ owner: 'team-a', ownerKey: 'team:team-a', consumers: 2, incompatible: 2 }, { owner: 'team-b', ownerKey: 'team:team-b', consumers: 1, incompatible: 1 }];
    const target = document.createElement('div');
    document.body.appendChild(target);
    const c = mount(ImpactFigure, { target, props: { impact: im } });
    flushSync();
    expect(target.querySelectorAll('[data-m="subject"]')).toHaveLength(1);
    expect(target.querySelector('[data-m="subject"]')?.classList.contains('t-neutral')).toBe(true);
    expect(target.querySelector('[data-m="next"]')).toBeNull();
    expect(target.querySelector('[data-m="prev"]')).toBeNull();
    expect([...target.querySelectorAll('.lane-name')].map((n) => n.textContent)).toEqual(['Breaks', 'May degrade', 'Unknown', 'Unread sources']);
    expect(target.querySelector('figcaption')?.textContent).toContain('If payments-service becomes unavailable: at least 2 consumers break and 1 may degrade.');
    const card = target.querySelector('[data-testid="changes-answer"]')!;
    expect(card.textContent?.replace(/\s+/g, ' ')).toContain('Gate Needs a person: 1 required consumer runs on prod/eu-1.');
    expect(card.querySelector('[data-testid="impact-changed-owner"] a')?.getAttribute('href')).toBe('#/fleet/owners/team%3Apay');
    expect(card.textContent?.replace(/\s+/g, ' ')).toContain('Question What happens if payments-service becomes unavailable?');
    expect(card.querySelector('[data-testid="impact-templates"]')).toBeNull();
    // No version is judged in an event: an owner's consumers break, never "incompatible".
    const owners = [...card.querySelectorAll('[data-testid="impact-owners"] li')].map((li) => li.textContent?.replace(/\s+/g, ' ').trim());
    expect(owners).toEqual(['team-a2 consumers, 2 break', 'team-b1 consumer, 1 breaks']);
    unmount(c);
  });
  it('lists the golden paths that reference a changed policy, never as consumers', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
    const im = impact();
    im.templates = { total: 3, count: 2, truncated: true, items: [
      { kind: 'service', key: 'http-path', label: 'http-path', href: '/fleet/services/http-path' },
      { kind: 'service', key: 'grpc-path', label: 'grpc-path', href: '/fleet/services/grpc-path' }] };
    const target = document.createElement('div');
    document.body.appendChild(target);
    const c = mount(ImpactFigure, { target, props: { impact: im } });
    flushSync();
    const row = target.querySelector('[data-testid="impact-templates"]')!;
    expect([...row.querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual(['#/fleet/services/http-path', '#/fleet/services/grpc-path']);
    expect(row.textContent?.replace(/\s+/g, ' ')).toContain('grpc-path and 1 more. They run nowhere, so they never hold the gate.');
    unmount(c);
  });
});
