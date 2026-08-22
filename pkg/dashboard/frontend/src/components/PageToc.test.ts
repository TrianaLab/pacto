/**
 * Render tests for PageToc.svelte — the ONE "On this page" navigator shared by every
 * long product page.
 *
 * What these pin down is the promise the control makes: every entry it offers is a
 * section the page ACTUALLY rendered, and following one lands the reader somewhere they
 * can see. A contents list that survives a section being removed, or that scrolls to a
 * heading still folded inside a closed disclosure, is worse than no contents list.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import PageToc from './PageToc.svelte';

// jsdom implements neither matchMedia nor scrollIntoView. The component treats both as
// optional (it must survive any host that lacks them), so the stub here exists to drive
// the breakpoint, not to keep the component from throwing.
type MQ = { matches: boolean; fire: (v: boolean) => void };
function stubMatchMedia(): Map<string, MQ> {
  const reg = new Map<string, MQ>();
  (window as any).matchMedia = (q: string) => {
    let e = reg.get(q);
    if (!e) {
      const ls = new Set<() => void>();
      e = {
        matches: false,
        fire: (v: boolean) => { (e as any).matches = v; ls.forEach((f) => f()); },
        addEventListener: (_: string, f: () => void) => ls.add(f),
        removeEventListener: (_: string, f: () => void) => ls.delete(f),
      } as any;
      reg.set(q, e!);
    }
    return e;
  };
  return reg;
}

const WIDE = '(min-width: 1100px)';
const REDUCE = '(prefers-reduced-motion: reduce)';

/** A page body carrying `n` tagged sections, plus whatever extra markup is given. */
function page(labels: string[], extra = ''): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = labels
    .map((l, i) => `<section id="sec-${i}" data-toc="${l}"><h2>${l}</h2></section>`)
    .join('') + extra;
  document.body.appendChild(el);
  return el;
}

const links = (t: HTMLElement) => Array.from(t.querySelectorAll('.toc-link')) as HTMLButtonElement[];
const labels = (t: HTMLElement) => links(t).map((b) => b.textContent?.trim());
/** MutationObserver callbacks are microtasks; let the queue drain, then re-render. */
const settle = async () => { await Promise.resolve(); await Promise.resolve(); flushSync(); };

/** The entries marked current — as a LIST, so "exactly one" is part of every assertion. */
const currentLabels = (t: HTMLElement) => links(t)
  .filter((b) => b.getAttribute('aria-current') === 'true')
  .map((b) => b.textContent?.trim());

/**
 * jsdom lays nothing out, so the sections are given the geometry the rule reads: a top
 * edge each, or `null` for a section with no box at all — which is what a heading folded
 * inside a closed disclosure looks like to `getClientRects`.
 */
function layout(tops: (number | null)[]) {
  tops.forEach((top, i) => {
    const el = document.getElementById(`sec-${i}`) as HTMLElement;
    (el as any).getClientRects = () => (top === null ? [] : [{ top }]);
    el.getBoundingClientRect = () => ({ top }) as DOMRect;
  });
}

/** One scroll, then one animation frame — the component coalesces to exactly that. */
const nextFrame = () => new Promise((r) => requestAnimationFrame(() => r(null)));
async function scrolled() {
  window.dispatchEvent(new Event('scroll'));
  await nextFrame();
  flushSync();
}

describe('PageToc', () => {
  let target: HTMLElement;
  let mq: Map<string, MQ>;
  let body: HTMLElement | null = null;

  /** Mount and let the discovery effect run: `$effect` is queued, not synchronous. */
  function mountToc(props: Record<string, unknown> = {}) {
    const c = mount(PageToc, { target, props });
    flushSync();
    return c;
  }

  beforeEach(() => {
    mq = stubMatchMedia();
    (Element.prototype as any).scrollIntoView = vi.fn();
    target = document.createElement('div');
    document.body.appendChild(target);
  });
  afterEach(() => {
    document.body.removeChild(target);
    if (body) { document.body.removeChild(body); body = null; }
    delete (window as any).matchMedia;
  });

  it('lists the sections the page actually rendered, in document order', () => {
    body = page(['Immediate situation', 'Operational posture', 'Organization and contract']);
    const c = mountToc();
    expect(labels(target)).toEqual(['Immediate situation', 'Operational posture', 'Organization and contract']);
    expect(target.querySelector('.toc-count')?.textContent).toBe('3');
    unmount(c);
  });

  it('stays out of the way on a page too short to need it', () => {
    body = page(['Only section', 'Second section']);
    const c = mountToc();
    expect(target.querySelector('[data-testid="page-toc"]')).toBeNull();
    unmount(c);
  });

  it('ignores a tagged element with no id — there would be nowhere to send the reader', () => {
    body = page(['A', 'B', 'C'], '<section data-toc="Untargetable"></section>');
    const c = mountToc();
    expect(labels(target)).toEqual(['A', 'B', 'C']);
    unmount(c);
  });

  it('follows the page when a section appears or disappears', async () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();
    expect(labels(target)).toEqual(['A', 'B', 'C']);

    const extra = document.createElement('section');
    extra.id = 'sec-late';
    extra.setAttribute('data-toc', 'Recent evidence');
    body.appendChild(extra);
    await settle();
    expect(labels(target)).toEqual(['A', 'B', 'C', 'Recent evidence']);

    body.querySelector('#sec-1')!.remove();
    await settle();
    expect(labels(target)).toEqual(['A', 'C', 'Recent evidence']);
    unmount(c);
  });

  it('navigates with buttons, never with a URL fragment', () => {
    body = page(['A', 'B', 'C']);
    const before = window.location.hash;
    const c = mountToc();
    for (const b of links(target)) {
      expect(b.tagName).toBe('BUTTON');
      expect(b.getAttribute('href')).toBeNull();
    }
    expect(target.querySelector('a')).toBeNull();
    links(target)[1].click();
    expect(window.location.hash).toBe(before);
    unmount(c);
  });

  /**
   * WHERE AM I. One rule, one answer: the current section is the last one whose top edge
   * has reached the reading line. Ordering by a single line cannot tie, so — unlike an
   * intersection ratio over the several sections a desktop viewport holds at once — it
   * cannot oscillate between two of them as the reader scrolls slowly.
   */
  it('marks the last section that has reached the reading line, and only that one', async () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();

    layout([-400, -10, 300]); // A and B are past the line, C is still below it
    await scrolled();
    expect(currentLabels(target)).toEqual(['B']);
    expect(target.querySelectorAll('.toc-link.current').length).toBe(1);

    layout([-900, -520, -5]); // the reader scrolls C up over the line
    await scrolled();
    expect(currentLabels(target)).toEqual(['C']);
    unmount(c);
  });

  /**
   * The line is where a section COMES TO REST when it is scrolled to — its own
   * scroll-margin-top, which exists so the heading clears the sticky app bar. Reading it
   * from the CSS is what makes clicking an entry and scrolling to it agree: at the pixel
   * the browser parks the section at, that section is the current one.
   */
  it('reads the reading line off the section’s own scroll-margin-top', async () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();
    const real = window.getComputedStyle;
    window.getComputedStyle = (() => ({ scrollMarginTop: '80px' })) as any;
    try {
      // B has cleared the app bar but not the viewport top: current under the CSS line,
      // and NOT current if the line were hard-coded to zero here.
      layout([-200, 40, 300]);
      await scrolled();
      expect(currentLabels(target)).toEqual(['B']);
    } finally { window.getComputedStyle = real; }
    unmount(c);
  });

  /**
   * And EACH section against its own line, not the first one's. `[data-toc]` carries a
   * shared default, but nothing forces a section to keep it — one parking under a sticky
   * sub-header clears more — and measuring every section against one section's margin
   * answers for a pixel the browser does not park the others at.
   */
  it('measures every visible section against its own line, not the first one’s', async () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();
    const real = window.getComputedStyle;
    window.getComputedStyle = ((el: Element) =>
      ({ scrollMarginTop: el.id === 'sec-2' ? '160px' : '0px' })) as any;
    try {
      // C has come to rest at ITS line (100 <= 160); A and B are past theirs. Under one
      // shared line taken from A, C reads as "still below" and B stays current — the
      // section the reader is actually looking at would not be the marked one.
      layout([-400, -10, 100]);
      await scrolled();
      expect(currentLabels(target)).toEqual(['C']);
    } finally { window.getComputedStyle = real; }
    unmount(c);
  });

  it('keeps a chosen entry current while the scroll travels, until the reader takes over', async () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();
    layout([-400, -10, 300]);
    await scrolled();
    expect(currentLabels(target)).toEqual(['B']);

    links(target)[2].click();
    flushSync();
    expect(currentLabels(target)).toEqual(['C']); // answered on the click, not on arrival

    // The smooth scroll passes over the sections in between, and a short last section may
    // never reach the line at all. Neither may undo what the reader asked for.
    layout([-100, 200, 900]);
    await scrolled();
    expect(currentLabels(target)).toEqual(['C']);

    // The reader driving is the signal that geometry is the truth again.
    window.dispatchEvent(new Event('wheel'));
    await nextFrame();
    flushSync();
    expect(currentLabels(target)).toEqual(['A']);
    unmount(c);
  });

  it('never makes a section current that the page has not laid out', async () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();

    // B is folded away. Its zeroed rect would read as "top: 0" and beat every real
    // section, which is exactly the wrong answer: it is not on screen to be read.
    layout([-400, null, 300]);
    await scrolled();
    expect(currentLabels(target)).toEqual(['A']);

    // Above everything, the first rendered section is current — the reader is in the
    // page's preamble, not nowhere.
    layout([200, null, 600]);
    await scrolled();
    expect(currentLabels(target)).toEqual(['A']);
    unmount(c);
  });

  it('opens every collapsed ancestor BEFORE scrolling, then parks the caret there', () => {
    body = page(['A', 'B'], `
      <details id="outer"><summary>Outer</summary>
        <details id="sec-deep" data-toc="Software inventory"><summary>Inventory</summary><p>x</p></details>
      </details>`);
    const c = mountToc();

    const deep = document.getElementById('sec-deep') as HTMLDetailsElement;
    const outer = document.getElementById('outer') as HTMLDetailsElement;
    expect(deep.open).toBe(false);
    expect(outer.open).toBe(false);

    links(target).find((b) => b.textContent?.trim() === 'Software inventory')!.click();

    expect(deep.open).toBe(true);
    expect(outer.open).toBe(true);
    expect(deep.scrollIntoView).toHaveBeenCalled();
    expect(deep.getAttribute('tabindex')).toBe('-1');
    expect(document.activeElement).toBe(deep);
    unmount(c);
  });

  it('honours a reduced-motion preference instead of animating the jump', () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();

    links(target)[0].click();
    expect((document.getElementById('sec-0') as any).scrollIntoView)
      .toHaveBeenLastCalledWith({ behavior: 'smooth', block: 'start' });

    mq.get(REDUCE)!.fire(true);
    links(target)[1].click();
    expect((document.getElementById('sec-1') as any).scrollIntoView)
      .toHaveBeenLastCalledWith({ behavior: 'auto', block: 'start' });
    unmount(c);
  });

  it('is an open rail where there is room and a closed disclosure where there is not', () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();
    const box = target.querySelector('.toc-box') as HTMLDetailsElement;

    // Narrow first: a contents list that eats the first screenful of a phone is a cost,
    // not a shortcut. Widen, and the same control becomes the rail.
    expect(box.open).toBe(false);
    mq.get(WIDE)!.fire(true);
    flushSync();
    expect(box.open).toBe(true);
    unmount(c);
  });

  it('keeps a reader’s own open/closed choice across a resize', () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc();
    const box = target.querySelector('.toc-box') as HTMLDetailsElement;
    expect(box.open).toBe(false);

    // Toggling comes from the <summary>, which is what the browser activates for both a
    // tap and a keyboard Enter — so one handler covers both.
    (target.querySelector('.toc-summary') as HTMLElement).click();
    flushSync();
    expect(box.open).toBe(true);

    (window as any).matchMedia(WIDE).fire(false);
    flushSync();
    expect(box.open).toBe(true);
    unmount(c);
  });

  it('names itself for assistive tech and can be renamed per page', () => {
    body = page(['A', 'B', 'C']);
    const c = mountToc({ label: 'Sections' });
    const nav = target.querySelector('nav.toc') as HTMLElement;
    expect(nav.getAttribute('aria-label')).toBe('Sections');
    expect(target.querySelector('.toc-title')?.textContent).toBe('Sections');
    unmount(c);
  });

  it('survives a host with neither matchMedia nor scrollIntoView', () => {
    delete (window as any).matchMedia;
    delete (Element.prototype as any).scrollIntoView;
    body = page(['A', 'B', 'C']);
    const c = mountToc();
    expect(labels(target)).toEqual(['A', 'B', 'C']);
    expect(() => links(target)[0].click()).not.toThrow();
    unmount(c);
  });
});
