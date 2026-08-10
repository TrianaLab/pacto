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
