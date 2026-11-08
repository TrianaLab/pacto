import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ChangeFigure from './ChangeFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import InboxFigure from './InboxFigure.svelte';
// @ts-expect-error — Svelte component has no declaration file
import Station from './Station.svelte';

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

afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ''; sessionStorage.clear(); });

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
  const href = (env: string, kind: string) => `#/delivery/inbox?environment=${env}&show=${kind}`;

  it('draws neutral stations, one queue per environment and links every count it can filter (T3, S10)', async () => {
    const { target, c } = render(InboxFigure, { byEnvironment, href }, false);
    await settle();
    expect([...target.querySelectorAll('.stn')].map((s) => s.getAttribute('class'))).toEqual(['stn mk t-neutral', 'stn mk t-neutral', 'stn mk t-neutral']);
    expect([...target.querySelectorAll('.pst-name')].map((n) => n.textContent)).toEqual(['test', 'prod', 'Stages you cannot read']);
    const rows = [...target.querySelectorAll('.qrow')];
    expect(rows.map((r) => r.textContent!.trim())).toEqual(['1 errored', '2 held', '1 held', '1 frozen', '3 held']);
    expect(rows.map((r) => r.getAttribute('href'))).toEqual([href('test', 'errored'), href('test', 'held'), href('prod', 'held'), href('prod', 'frozen'), null]);
    expect(rows[4].hasAttribute('data-nofilter')).toBe(true);
    expect(target.querySelector('figcaption')?.textContent).toContain('1 errored change, 6 held changes and 1 frozen Stage wait for you.');
    expect(target.querySelector('[data-m="qb1frozen"]')?.classList.contains('t-frost')).toBe(true);
    unmount(c);
    const p = render(InboxFigure, { byEnvironment, href, partial: true }, false);
    expect(p.target.querySelector('figcaption')?.textContent).toContain('Namespaces you cannot list are not counted, so each queue may hold more.');
    unmount(p.c);
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
