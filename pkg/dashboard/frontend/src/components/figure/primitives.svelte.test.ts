import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, createRawSnippet } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import Wire from './Wire.svelte';
// @ts-expect-error — Svelte component has no declaration file
import Ghost from './Ghost.svelte';
// @ts-expect-error — Svelte component has no declaration file
import Lane from './Lane.svelte';
// @ts-expect-error — Svelte component has no declaration file
import AnswerCard from './AnswerCard.svelte';
// @ts-expect-error — Svelte component has no declaration file
import CountTile from './CountTile.svelte';
// @ts-expect-error — Svelte component has no declaration file
import Glyph from './Glyph.svelte';
// @ts-expect-error — Svelte component has no declaration file
import EnvMark from './EnvMark.svelte';
import { ENV_STATES } from '../../lib/envFigure.ts';
import { KIND_GLYPH } from '../../lib/figure.ts';

// The SPEC 3 primitives on their own: what each draws, and which words it never carries.
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- props of untyped components
function render(C: any, props: Record<string, unknown>, svg = false) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const target = svg ? host.appendChild(document.createElementNS('http://www.w3.org/2000/svg', 'svg')) : host;
  const c = mount(C, { target, props });
  return { host, c };
}
const snippet = (html: string) => createRawSnippet(() => ({ render: () => html }));

afterEach(() => { document.body.innerHTML = ''; });

describe('Wire', () => {
  it('draws its style, the broken ✕ and the arrowhead, and no words', () => {
    const { host, c } = render(Wire, { w: { id: 'w1', d: 'M0 0 L10 0', off: false, style: 'broken', tone: 'err', mark: 'M1 1 L2 2', head: 'M10 0 L3 4 L3 -4 Z' } }, true);
    expect(host.querySelector('path.w')?.getAttribute('class')).toContain('w--broken');
    expect(host.querySelector('path.w-x')?.getAttribute('d')).toBe('M1 1 L2 2');
    expect(host.querySelector('path.w-head')?.getAttribute('d')).toBe('M10 0 L3 4 L3 -4 Z');
    expect(host.querySelector('g.wire')?.getAttribute('class')).toContain('t-err');
    expect(host.querySelector('path.sig')?.getAttribute('d')).toBe('M0 0 L10 0');
    expect(host.textContent).toBe('');
    unmount(c);
  });
  it('keeps an unplaced wire as hidden paths, with no marks', () => {
    const { host, c } = render(Wire, { w: { id: 'w2', d: '', off: true, style: 'proj', tone: 'accent', mark: 'M1 1', head: 'M2 2' } }, true);
    expect((host.querySelector('path.w') as SVGElement).style.display).toBe('none');
    expect(host.querySelector('.w-x, .w-head')).toBeNull();
    unmount(c);
  });
});

describe('Ghost and Lane', () => {
  it('counts what the reader may not see, never names it and never links it', () => {
    const { host, c } = render(Ghost, { k: 3, n: 12, note: 'Names hidden by your access. Not counted in the 400.' });
    expect(host.querySelector('.lane.ghost .lane-count')?.hasAttribute('data-nofilter')).toBe(true);
    expect(host.querySelector('.lane-count')?.textContent).toBe('12');
    expect(host.querySelector('a, .chip, .mbar')).toBeNull();
    expect(host.querySelector('.nd.unk')).toBeTruthy();
    expect(host.querySelector('.lane-sub')?.textContent).toBe('Names hidden by your access. Not counted in the 400.');
    unmount(c);
  });
  it("links a lane's count and 'List all N' to its list, and says no filter when nothing lists it", () => {
    const linked = render(Lane, { k: 0, tone: 'err', label: 'Incompatible', n: 37, total: 1284, href: '#/x?outcome=incompatible' });
    expect(linked.host.querySelector('a.lane-count')?.getAttribute('href')).toBe('#/x?outcome=incompatible');
    expect(linked.host.querySelector('a.lane-more')?.textContent).toBe('List all 37');
    unmount(linked.c);
    const bare = render(Lane, { k: 0, tone: 'ok', label: 'Compatible', n: 0, total: 1284, href: '#/x' });
    expect(bare.host.querySelector('.lane-count')?.hasAttribute('data-nofilter')).toBe(true);
    expect(bare.host.querySelector('a')).toBeNull();
    unmount(bare.c);
  });
});

describe('AnswerCard', () => {
  it("states the caller's facts, then hidden, evidence, asOf and provenance in one list", () => {
    const { host, c } = render(AnswerCard, {
      stamps: [{ label: 'Breaking', tone: 'err' }], hidden: 3, completeness: 'partial', asOf: '12:04', provenance: 'Contract diff',
      facts: snippet('<dt>Change</dt>'), lead: snippet('<p class="lead">3 changes</p>'),
    });
    expect([...host.querySelectorAll('dl.facts dt')].map((d) => d.textContent)).toEqual(['Change', 'You may not see', 'Evidence', 'As of', 'Provenance']);
    expect(host.querySelector('.stamp.t-err')?.textContent).toBe('Breaking');
    expect(host.querySelector('.lead')?.textContent).toBe('3 changes');
    expect(host.textContent).toContain('3, not counted and never named');
    unmount(c);
    const plain = render(AnswerCard, { completeness: 'complete', provenance: 'p', facts: snippet('<dt>X</dt>') });
    expect([...plain.host.querySelectorAll('dl.facts dt')].map((d) => d.textContent)).toEqual(['X', 'Evidence', 'Provenance']);
    expect(plain.host.querySelector('.ac-stamps')).toBeNull();
    unmount(plain.c);
  });
});

describe('CountTile and Glyph', () => {
  it('links what a cap leaves out and never links or totals the hidden', () => {
    const { host, c } = render(CountTile, { more: 312, href: '#/fleet/services?x=1', parts: ['40 direct'], hidden: 3 });
    expect(host.querySelector('a')?.textContent).toBe('+312 more');
    expect(host.querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?x=1');
    expect([...host.querySelectorAll('.count-tile-part')].map((p) => p.textContent)).toEqual(['40 direct', '3 you may not see']);
    expect(host.querySelector('[data-nofilter]')?.textContent).toBe('3 you may not see');
    unmount(c);
    const none = render(CountTile, { more: 0, href: '#/x' });
    expect(none.host.querySelector('a')).toBeNull();
    unmount(none.c);
  });
  it('draws each kind as decoration, in two sizes, with no tone', () => {
    for (const kind of Object.keys(KIND_GLYPH)) {
      const { host, c } = render(Glyph, { kind, size: 'md' });
      const svg = host.querySelector('svg')!;
      expect(svg.getAttribute('aria-hidden')).toBe('true');
      expect(svg.getAttribute('class')).toBe('kind-glyph kind-glyph--md');
      expect(svg.querySelector('path')?.getAttribute('d')).toBe(KIND_GLYPH[kind as keyof typeof KIND_GLYPH]);
      unmount(c);
    }
    const sm = render(Glyph, { kind: 'service' });
    expect(sm.host.querySelector('svg')?.getAttribute('class')).toBe('kind-glyph kind-glyph--sm');
    unmount(sm.c);
  });
});

describe('EnvMark', () => {
  it('draws the board mark for every state and names it in words', () => {
    for (const st of [...ENV_STATES.map((x) => x.key), 'unknown']) {
      const { host, c } = render(EnvMark, { state: st });
      const m = host.querySelector('[data-testid="env-state-mark"]')!;
      expect(m.getAttribute('data-state')).toBe(st);
      expect(m.querySelector('.ev-dot')?.classList.contains(`s-${st}`)).toBe(true);
      expect(m.getAttribute('aria-label')).toBe(st === 'unknown' ? 'not known: its operations could not be read' : ENV_STATES.find((x) => x.key === st)!.word);
      expect(m.tagName).toBe('SPAN');
      unmount(c);
    }
  });
  it('links when given an href, and shows the word instead of an aria-label when asked', () => {
    const linked = render(EnvMark, { state: 'failed', href: '#/x' });
    const a = linked.host.querySelector('a[data-testid="env-state-mark"]')!;
    expect(a.getAttribute('href')).toBe('#/x');
    expect(a.getAttribute('aria-label')).toBe('failed');
    unmount(linked.c);
    const worded = render(EnvMark, { state: 'empty', word: true });
    const w = worded.host.querySelector('[data-testid="env-state-mark"]')!;
    expect(w.textContent).toBe('running nothing yet');
    expect(w.hasAttribute('aria-label')).toBe(false);
    unmount(worded.c);
  });
});
