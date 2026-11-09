import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ChangeFigure from './ChangeFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import InboxFigure from './InboxFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import Station from './Station.svelte';
// @ts-expect-error — Svelte component has no declaration file
import EnvFleetFigure from './EnvFleetFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import PromotionFigure from './PromotionFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import HistoryFigure from './HistoryFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import AutonomyFigure from './AutonomyFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import PublishFigure from './PublishFigure.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;
const FROM = `sha256:${'1'.repeat(64)}`, TO = `sha256:${'2'.repeat(64)}`;
const held = (): Any => ({
  explanation: {
    operation: 'pay-promote-1', summary: '', spec: { type: 'delivery.pacto.trianalab.io/promote', stage: 'payments', params: { from: FROM, to: TO }, actor: { user: 'a' } },
    status: { phase: 'Held' }, verdicts: [], stage: { name: 'payments' },
  },
  operation: { metadata: { name: 'pay-promote-1', namespace: 'prod' }, spec: {}, status: { phase: 'Held', guarded: true, plan: { verdicts: [{ result: 'Pass' }], floor: { human: true } } } },
  versions: { [FROM]: '2.0.0', [TO]: '2.0.1' }, upstream: { kind: 'stage', namespace: 'test', stage: 'payments', environment: 'test' }, actions: [],
});

function render(C: Any, props: Any, reduced: boolean) {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: reduced && q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
  const target = document.createElement('div');
  document.body.appendChild(target);
  return { target, c: mount(C, { target, props }) };
}
const stage = (t: HTMLElement) => t.querySelector('.fig-stage')!.innerHTML;
const settle = async () => { for (let i = 0; i < 5; i++) await tick(); };

afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ''; sessionStorage.clear(); localStorage.clear(); });

describe('ChangeFigure', () => {
  it('renders its final frame under reduced motion too, the token at rest on the gate (G2)', async () => {
    const a = render(ChangeFigure, { data: held(), namespace: 'prod' }, false);
    const b = render(ChangeFigure, { data: held(), namespace: 'prod' }, true);
    await settle();
    expect(stage(b.target)).toBe(stage(a.target));
    expect(b.target.textContent).toContain('Step 5 of 5');
    expect(a.target.querySelectorAll('.tok')).toHaveLength(1);
    expect(a.target.querySelector('[data-tok="4"] .tok.t-warn')).toBeTruthy();
    expect(a.target.querySelector('.badge.here')?.textContent).toBe('2.0.1 is here');
    expect(a.target.querySelectorAll('.rl-off')).toHaveLength(2);
    expect(a.target.querySelector('figure svg text')).toBeNull();
    expect(a.target.querySelector('figcaption')?.textContent).toContain('“2.0.1 is here”');
    // Stepping back moves the token to the station of that step and hides what is ahead.
    ([...b.target.querySelectorAll('button')].find((x) => x.textContent === 'Previous') as HTMLButtonElement).click();
    flushSync();
    expect(b.target.textContent).toContain('Step 4 of 5');
    expect(b.target.querySelector('[data-m="f4"]')?.classList.contains('nar-hid')).toBe(true);
    ([...b.target.querySelectorAll('button')].find((x) => x.textContent === 'Next') as HTMLButtonElement).click();
    flushSync();
    expect(stage(b.target)).toBe(stage(a.target));
    unmount(a.c); unmount(b.c);
  });
});

describe('InboxFigure', () => {
  const byEnvironment = [
    { environment: 'test', errored: 1, held: 2, frozen: 0, upstream: [] },
    { environment: 'prod', errored: 0, held: 1, frozen: 1, upstream: ['test'] },
    { environment: '', errored: 0, held: 3, frozen: 0, upstream: [] },
  ];
  const href = (env: string, kind: string, except?: string[]) => `#/delivery/inbox?environment=${env}&show=${kind}${except ? `&except=${except.join('|')}` : ''}`;

  it('draws neutral stations, one queue per environment and links every count it can filter (T3, S10)', async () => {
    const { target, c } = render(InboxFigure, { byEnvironment, href }, false);
    await settle();
    expect([...target.querySelectorAll('.stn')].map((s) => s.getAttribute('class'))).toEqual(['stn mk t-neutral', 'stn mk t-neutral', 'stn mk t-neutral']);
    expect([...target.querySelectorAll('.pst-name')].map((n) => n.textContent)).toEqual(['test', 'prod', 'Stages you cannot read']);
    // An environment's name is an identity: one line, elided by segment, the whole name in its title.
    expect([...target.querySelectorAll('.pst-name')].map((n) => n.getAttribute('title'))).toEqual(['test', 'prod', null]);
    const rows = [...target.querySelectorAll('.qrow')];
    expect(rows.map((r) => r.textContent!.trim())).toEqual(['1 errored', '2 held', '1 held', '1 frozen', '3 held']);
    expect(rows.map((r) => r.getAttribute('href'))).toEqual([href('test', 'errored'), href('test', 'held'), href('prod', 'held'), href('prod', 'frozen'), null]);
    expect(rows[4].hasAttribute('data-nofilter')).toBe(true);
    expect(target.querySelector('figcaption')?.textContent).toContain('1 errored change, 6 held changes and 1 frozen Stage wait for you.');
    expect(target.querySelector('[data-m="qb1frozen"]')?.classList.contains('t-frost')).toBe(true);
    expect(target.querySelectorAll('.opencap')).toHaveLength(0);
    unmount(c);
    const p = render(InboxFigure, { byEnvironment, href, partial: true }, false);
    expect(p.target.querySelector('figcaption')?.textContent).toContain('Namespaces you cannot list are not counted, so each queue may hold more.');
    // S5a: every queue bar ends open, with the same glyph beside the sentence.
    expect(p.target.querySelectorAll('.qrow .opencap')).toHaveLength(5);
    expect(p.target.querySelectorAll('figcaption .opencap')).toHaveLength(1);
    unmount(p.c);
  });

  it('links the folded station to the lists of every environment it folds (S2, T3)', async () => {
    const many = Array.from({ length: 9 }, (_, i) => ({ environment: `e${i}`, errored: i + 1, held: 0, frozen: 0, upstream: [] }));
    const { target, c } = render(InboxFigure, { byEnvironment: many, href }, false);
    await settle();
    const fold = [...target.querySelectorAll('.station')].at(-1)!;
    const drawn = ['e3', 'e4', 'e5', 'e6', 'e7', 'e8'];
    expect(fold.querySelector('a.pst-name')?.textContent).toBe('3 more environments');
    expect(fold.querySelector('a.pst-name')?.getAttribute('href')).toBe(href('', '', drawn));
    expect(fold.querySelector('a.qrow')?.getAttribute('href')).toBe(href('', 'errored', drawn));
    expect(fold.querySelector('a.qrow')?.textContent?.trim()).toBe('6 errored');
    unmount(c);
  });

  it('folds the drawing away with a remembered Compact toggle, keeping the caption (L3)', async () => {
    const a = render(InboxFigure, { byEnvironment, href }, false);
    await settle();
    const btn = () => [...document.querySelectorAll('button')].find((x) => x.textContent === 'Compact') as HTMLButtonElement;
    expect(btn().getAttribute('aria-pressed')).toBe('false');
    btn().click();
    await settle();
    const fig = a.target.querySelector('figure')!;
    expect(fig.hasAttribute('data-compact')).toBe(true);
    expect(btn().getAttribute('aria-pressed')).toBe('true');
    expect([...a.target.querySelectorAll('button')].map((x) => x.textContent)).toEqual(['Compact']);
    expect(a.target.querySelector('figcaption')?.textContent).toContain('wait for you');
    expect(JSON.parse(localStorage.getItem('pacto-fig-compact')!)).toEqual(['inbox']);
    unmount(a.c);
    // Remembered: the next visit starts folded, and plays nothing.
    const b = render(InboxFigure, { byEnvironment, href }, false);
    await settle();
    expect(b.target.querySelector('figure')?.hasAttribute('data-compact')).toBe(true);
    expect(b.target.querySelector('figure')?.getAttribute('data-narration')).toBe('done');
    btn().click();
    await settle();
    expect(b.target.querySelector('figure')?.hasAttribute('data-compact')).toBe(false);
    expect(localStorage.getItem('pacto-fig-compact')).toBe('[]');
    unmount(b.c);
    // Storage that throws still folds, unremembered.
    vi.stubGlobal('localStorage', { getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); }, clear() {} });
    const d = render(InboxFigure, { byEnvironment, href }, false);
    await settle();
    btn().click();
    await settle();
    expect(d.target.querySelector('figure')?.hasAttribute('data-compact')).toBe(true);
    unmount(d.c);
  });

  it('renders the same drawing under reduced motion (G2)', async () => {
    const a = render(InboxFigure, { byEnvironment, href }, false);
    const b = render(InboxFigure, { byEnvironment, href }, true);
    await settle();
    expect(stage(b.target)).toBe(stage(a.target));
    expect(b.target.textContent).toContain('Step 2 of 2');
    unmount(a.c); unmount(b.c);
  });
});

describe('Station', () => {
  it('anchors on its four sides and carries no words', () => {
    const { target, c } = render(Station, { a: 'p0', m: 'p0' }, false);
    expect([...target.querySelectorAll('[data-a]')].map((x) => x.getAttribute('data-a'))).toEqual(['p0-t', 'p0-l', 'p0-r', 'p0-b']);
    expect(target.textContent!.trim()).toBe('');
    unmount(c);
  });
});

describe('EnvFleetFigure', () => {
  const byEnvironment = [
    { environment: 'test', tier: 'test', stages: 3, failed: 0, frozen: 0, autoHold: 0, waiting: 0, unverified: 1, empty: 0, verified: 2, upstream: [] },
    { environment: 'prod', tier: 'prod', stages: 2, failed: 1, frozen: 1, autoHold: 0, waiting: 0, unverified: 0, empty: 0, verified: 0, upstream: ['test'] },
  ];
  const href = (st: string, env: string, except?: string[]) => `#/delivery/environments?state=${st}&environment=${env}${except ? `&except=${except.join('|')}` : ''}`;

  it('draws neutral stations and links every count to the board filtered to it (S10, T3)', async () => {
    const { target, c } = render(EnvFleetFigure, { byEnvironment, href }, false);
    await settle();
    expect([...target.querySelectorAll('.stn')].map((x) => x.getAttribute('class'))).toEqual(['stn mk t-neutral', 'stn mk t-neutral']);
    expect([...target.querySelectorAll('.envc a')].map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['1', href('unverified', 'test')], ['2', href('verified', 'test')], ['1', href('failed', 'prod')], ['1', href('frozen', 'prod')]]);
    expect(target.querySelector('.envc .qglyph.unk')).toBeTruthy();
    expect(target.querySelector('.queue-h')?.textContent).toBe('3 Stages, named like test');
    expect(target.querySelector('.queue-h a')?.getAttribute('href')).toBe(href('', 'test'));
    expect(target.querySelector('figcaption')?.textContent).toContain('5 Stages in 2 environments: 1 failed, 0 waiting for a person and 1 frozen.');
    expect(target.querySelector('figure svg text')).toBeNull();
    expect(target.querySelectorAll('.opencap')).toHaveLength(0);
    unmount(c);
  });

  it('ends every share bar in an open cap when the listing is partial (S5a)', async () => {
    const { target, c } = render(EnvFleetFigure, { byEnvironment, href, partial: true }, false);
    await settle();
    expect(target.querySelector('figcaption')?.textContent).toContain('Namespaces you cannot list are not counted, so each environment may hold more Stages.');
    expect(target.querySelectorAll('.sbar-row .opencap')).toHaveLength(2);
    expect(target.querySelectorAll('figcaption .opencap')).toHaveLength(1);
    unmount(c);
  });

  it('links the folded station and its counts to the board of every environment it folds (S2, T3)', async () => {
    const many = Array.from({ length: 9 }, (_, i) => ({ ...byEnvironment[0], environment: `e${i}`, failed: i + 1, stages: i + 4 }));
    const { target, c } = render(EnvFleetFigure, { byEnvironment: many, href }, false);
    await settle();
    const fold = [...target.querySelectorAll('.station')].at(-1)!;
    const drawn = ['e3', 'e4', 'e5', 'e6', 'e7', 'e8'];
    expect(fold.querySelector('a.pst-name')?.textContent).toBe('3 more environments');
    expect(fold.querySelector('a.pst-name')?.getAttribute('href')).toBe(href('', '', drawn));
    expect(fold.querySelector('.queue-h a')?.getAttribute('href')).toBe(href('', '', drawn));
    expect(fold.querySelector('.envc a')?.getAttribute('href')).toBe(href('failed', '', drawn));
    unmount(c);
  });
});

describe('PromotionFigure', () => {
  const graph = {
    nodes: [
      { namespace: 'dev', stage: 'pay', environment: 'dev', level: 0, upstream: { kind: 'artifact', repository: 'pay-candidates' } },
      { namespace: 'prod', stage: 'pay', environment: 'prod', level: 1, upstream: { kind: 'stage', namespace: 'dev', stage: 'pay', environment: 'dev' } },
      { namespace: 'dr', stage: 'pay', environment: 'dr', level: 0, upstream: { kind: 'none' } },
    ],
    edges: [{ from: 'dev/pay', to: 'prod/pay' }],
  };
  const rows = [
    { namespace: 'dev', stage: 'pay', service: 'pay', environment: 'dev', pointer: FROM, version: '2.0.1', verified: true, failed: false, frozen: false, autoHold: false, held: 0, actions: [] },
    { namespace: 'prod', stage: 'pay', service: 'pay', environment: 'prod', pointer: FROM, version: '2.0.0', verified: true, failed: false, frozen: true, autoHold: false, held: 0, actions: [] },
  ];

  it('draws the service\'s own path, its entry and a hatched source it cannot follow (SPEC 21)', async () => {
    const { target, c } = render(PromotionFigure, { service: 'pay', graph, rows }, false);
    await settle();
    expect(target.querySelector('.fig-q')?.textContent).toBe('Where is pay on its way to production?');
    expect([...target.querySelectorAll('[data-testid="promotion-station"] .pg-l > b')].map((b) => b.textContent)).toEqual(['dev', 'prod', 'dr']);
    expect(target.querySelector('.pg-src:not(.pg-unk)')?.textContent).toContain('New artifacts');
    expect(target.querySelector('.pg-unk .nd.unk')).toBeTruthy();
    expect(target.querySelector('.pg-unk [data-a]')).toBeNull();
    expect(target.querySelector('.stamp.t-frost')?.textContent).toBe('Frozen');
    expect(target.querySelectorAll('.stn.t-neutral')).toHaveLength(3);
    expect(target.textContent).toContain('not on this page of the board');
    expect(target.querySelector('figure svg text')).toBeNull();
    unmount(c);
  });

  it('badges the two Stages of the last promotion and offers to replay it, never autoplaying', async () => {
    const last = { to: 'prod/pay', from: 'dev/pay', digest: FROM, started: '2026-11-08T10:00:00Z', finished: '2026-11-08T12:00:00Z' };
    const { target, c } = render(PromotionFigure, { service: 'pay', graph: { ...graph, last }, rows }, false);
    await settle();
    expect(target.querySelector('[data-testid="promoted-from"]')?.closest('[data-testid="promotion-station"]')?.textContent).toContain('dev');
    expect(target.querySelector('[data-testid="promoted-here"]')?.closest('[data-testid="promotion-station"]')?.textContent).toContain('prod');
    expect([...target.querySelectorAll('button')].some((b) => b.textContent === 'Replay')).toBe(true);
    expect(target.querySelector('figure')?.getAttribute('data-narration')).not.toBe('playing');
    unmount(c);
  });

  it('links the Stages past the cap to the board that lists them (S2)', async () => {
    const nodes = Array.from({ length: 12 }, (_, i) => ({ namespace: `e${i}`, stage: 'pay', environment: `e${i}`, level: i,
      upstream: i ? { kind: 'stage', namespace: `e${i - 1}`, stage: 'pay', environment: `e${i - 1}` } : { kind: 'artifact', repository: 'r' } }));
    const edges = nodes.slice(1).map((_, i) => ({ from: `e${i}/pay`, to: `e${i + 1}/pay` }));
    const moreHref = (ex: string[]) => `#/delivery/environments?except=${ex.join('|')}`;
    const { target, c } = render(PromotionFigure, { service: 'pay', graph: { nodes, edges }, rows: [], moreHref }, false);
    await settle();
    const shown = target.querySelectorAll('[data-testid="promotion-station"]').length;
    const a = target.querySelector('figcaption a');
    expect(a?.textContent).toBe(`${12 - shown} more Stages`);
    expect(a?.getAttribute('href')).toBe(moreHref(nodes.slice(0, shown).map((n) => n.environment)));
    unmount(c);
  });
});

describe('HistoryFigure', () => {
  const zero = { missing: 0, failed: 0, held: 0, acceptedUnknown: 0, released: 0, other: 0 };
  const buckets = Array.from({ length: 12 }, (_, i) => ({ start: `2026-11-08T${String(i * 2).padStart(2, '0')}:00:00Z`, end: `2026-11-08T${String(i * 2 + 1).padStart(2, '0')}:59:00Z`, ...zero,
    ...(i === 3 ? { missing: 1, failed: 2 } : i === 5 ? { released: 3 } : {}) }));
  const timeline = { start: buckets[0].start, end: '2026-11-08T23:59:00Z', buckets, totals: { ...zero, missing: 1, failed: 2, released: 3 }, untimed: 0 };
  const href = (s: string, u: string) => `#/delivery/history?since=${s}&until=${u}`;

  it('draws twelve columns, one mark each, links each count to its slice and marks a missing record with ⊗', async () => {
    const { target, c } = render(HistoryFigure, { timeline, href, source: 'records', live: true }, false);
    await settle();
    expect(target.querySelectorAll('.tl-bar.mk')).toHaveLength(12);
    expect([...target.querySelectorAll('a.tl-n')].map((a) => [a.textContent, a.getAttribute('href')])).toEqual([['2', href(buckets[3].start, buckets[3].end)], ['3', href(buckets[5].start, buckets[5].end)]]);
    expect(target.querySelectorAll('.tl-n[data-nofilter]')).toHaveLength(10);
    expect(target.querySelectorAll('.tl-c.gap .tl-x')).toHaveLength(1);
    expect(target.querySelector('.tl-gaps')?.textContent).toBe('Missing: one record between 06:00 and 07:59.');
    expect(target.querySelector('figure svg text')).toBeNull();
    unmount(c);
    const cl = render(HistoryFigure, { timeline, href, source: 'cluster', live: true }, false);
    await settle();
    expect(cl.target.querySelectorAll('.tl-x')).toHaveLength(0);
    expect(cl.target.querySelector('.keyl')?.textContent).toContain("shown on one Stage's history");
  });
});

describe('AutonomyFigure', () => {
  it('draws same-size cards, one mark each, and keeps not recorded apart from automatic', async () => {
    const byLevel = [{ level: 'observe', stages: 1 }, { level: 'advise', stages: 4 }, { level: 'auto-nonbreaking', stages: 2 }, { level: 'autonomous', stages: 0 }, { level: '', stages: 1 }];
    const { target, c } = render(AutonomyFigure, { byLevel, promotions: { count: 1, of: 3, untyped: 1 } }, false);
    await settle();
    expect(target.querySelectorAll('.aflow.mk')).toHaveLength(4);
    expect([...target.querySelectorAll('.aflow')].map((s) => s.getAttribute('width'))).toEqual(['168', '168', '168', '168']);
    expect([...target.querySelectorAll('.lv-t')].map((b) => b.textContent)).toEqual(['Observe', 'Advise', 'Auto non-breaking', 'Autonomous']);
    expect([...target.querySelectorAll('.keyl b')].map((b) => b.textContent)).toEqual(['2', '1', '1']);
    expect(target.querySelector('.lad-note')?.textContent).toContain('1 Stage reports no level.');
    expect(target.querySelector('figure svg text')).toBeNull();
    unmount(c);
  });

  it('links each level count to the Stages it counts and outlines the fleet default (T3)', async () => {
    const byLevel = [{ level: 'observe', stages: 1 }, { level: 'advise', stages: 4 }, { level: 'auto-nonbreaking', stages: 2 }, { level: 'autonomous', stages: 0 }, { level: '', stages: 1 }];
    const href = (l: string) => `#/delivery/autonomy?level=${l}`;
    const { target, c } = render(AutonomyFigure, { byLevel, promotions: { count: 1, of: 3, untyped: 0 }, href }, false);
    await settle();
    expect([...target.querySelectorAll('.lv a')].map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['1', href('observe')], ['4', href('advise')], ['2', href('auto-nonbreaking')], ['0', href('autonomous')]]);
    expect(target.querySelector('.lad-note a')?.getAttribute('href')).toBe(href('none'));
    expect([...target.querySelectorAll('.lv.lv-def .lv-t')].map((b) => b.textContent)).toEqual(['Observe']);
    unmount(c);
  });
});

describe('PublishFigure', () => {
  it('partitions the services into two lanes whose counts open the lists they count (T3)', async () => {
    const counts = { services: 40, versions: 120, signed: 110, unsigned: 10, unsignedServices: 6, firstUnsigned: ['a', 'b', 'c', 'd'] };
    const href = (s: string) => `#/delivery/publish?signed=${s}`;
    const { target, c } = render(PublishFigure, { counts, href, serviceHref: (n: string) => `#/fleet/services/${n}` }, false);
    await settle();
    const lanes = [...target.querySelectorAll('.lane')];
    expect(lanes.map((l) => l.querySelector('.lane-name')?.textContent)).toEqual(['Every version signed', 'An unsigned version']);
    expect(lanes.map((l) => l.querySelector('a.lane-count')?.getAttribute('href'))).toEqual([href('yes'), href('no')]);
    expect(lanes.map((l) => l.querySelector('.lane-count b')?.textContent)).toEqual(['34', '6']);
    expect([...lanes[1].querySelectorAll('.chip')].map((x) => x.getAttribute('href'))).toEqual(['#/fleet/services/a', '#/fleet/services/b', '#/fleet/services/c', '#/fleet/services/d']);
    expect(target.querySelector('figcaption')?.textContent).toContain('6 services of 40 have a version that is not signed, 10 versions in all');
    expect(target.querySelector('figure svg text')).toBeNull();
    unmount(c);
    const all = render(PublishFigure, { counts: { ...counts, unsigned: 0, unsignedServices: 0, firstUnsigned: [] }, href, serviceHref: (n: string) => n }, false);
    await settle();
    expect(all.target.querySelector('figcaption')?.textContent).toContain('Every version of the 40 services is signed.');
    expect(all.target.querySelectorAll('.lane').length).toBe(2);
    unmount(all.c);
    // A signature Pacto cannot tell is its own neutral lane, never counted as unsigned.
    const unk = render(PublishFigure, { counts: { ...counts, signed: 117, unsigned: 0, unknown: 3, unsignedServices: 0, unknownServices: 2, firstUnsigned: [] }, href, serviceHref: (n: string) => n }, false);
    await settle();
    const ul = [...unk.target.querySelectorAll('.lane')];
    expect(ul.map((l) => l.querySelector('.lane-name')?.textContent)).toEqual(['Every version signed', 'An unsigned version', 'Signature not known here']);
    expect(ul.map((l) => l.querySelector('.lane-count b')?.textContent)).toEqual(['38', '0', '2']);
    expect(ul[2].querySelector('a.lane-count')?.getAttribute('href')).toBe(href('unknown'));
    expect(unk.target.querySelector('figcaption')?.textContent).toContain('No version is known to be unsigned; the signature of 3 versions of 2 services is not known here.');
    expect(unk.target.querySelector('figcaption')?.textContent).not.toContain('cannot be traced');
  });
});
