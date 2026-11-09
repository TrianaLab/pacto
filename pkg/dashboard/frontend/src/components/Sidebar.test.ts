/**
 * The B2 sidebar (SPEC 20): grouped, collapsible to an icon rail, stored per browser
 * from 1200px, an overlay sheet from 768 to 1199px, gone under 768px.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { navGroups } from '../lib/nav';

// @ts-expect-error — Svelte component has no declaration file
import Sidebar from './Sidebar.svelte';

function setWidth(w: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: w });
  window.dispatchEvent(new Event('resize'));
}

let target: HTMLElement;
let component: ReturnType<typeof mount> | null = null;
function render(width: number, props: Record<string, unknown> = {}) {
  setWidth(width);
  target = document.createElement('div');
  document.body.appendChild(target);
  component = mount(Sidebar, {
    target,
    props: { groups: navGroups({ fleet: true, delivery: true }), view: 'fleet-services', ...props },
  });
  flushSync();
}
const side = () => target.querySelector('[data-testid="sidebar"]') as HTMLElement | null;
const toggle = () => target.querySelector('.side-toggle') as HTMLButtonElement;
const key = (k: string, init: KeyboardEventInit = {}, el: EventTarget = window) => {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, ...init }));
  flushSync();
};

beforeEach(() => { localStorage.clear(); });
afterEach(() => {
  if (component) unmount(component);
  component = null;
  target?.remove();
  document.body.innerHTML = '';
});

describe('Sidebar — default state by width', () => {
  it.each([
    [1600, 'open', 'true'],
    [1440, 'open', 'true'],
    [1280, 'rail', 'false'],
    [1024, 'rail', 'false'],
    [768, 'rail', 'false'],
  ])('at %ipx it starts %s', (w, mode, expanded) => {
    render(w);
    expect(side()?.dataset.rail).toBe(mode);
    expect(toggle().getAttribute('aria-expanded')).toBe(expanded);
    expect(toggle().getAttribute('aria-controls')).toBe('primary-sidebar');
    expect(side()?.id).toBe('primary-sidebar');
  });

  it('is gone under 768px', () => {
    render(767);
    expect(side()).toBeNull();
  });

  it('follows the window as it resizes', () => {
    render(1600);
    setWidth(1300); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
    setWidth(500); flushSync();
    expect(side()).toBeNull();
  });
});

describe('Sidebar — toggle, "[" and the stored choice', () => {
  it('the button collapses and expands, and the choice is stored from 1200px', () => {
    render(1600);
    toggle().click(); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
    expect(side()?.classList.contains('anim')).toBe(true);
    expect(localStorage.getItem('pacto-nav')).toBe('collapsed');
    toggle().click(); flushSync();
    expect(side()?.dataset.rail).toBe('open');
    expect(localStorage.getItem('pacto-nav')).toBe('expanded');
  });

  it('a stored choice wins over the width default', () => {
    localStorage.setItem('pacto-nav', 'expanded');
    render(1280);
    expect(side()?.dataset.rail).toBe('open');
    unmount(component!); component = null; target.remove();
    localStorage.setItem('pacto-nav', 'collapsed');
    render(1920);
    expect(side()?.dataset.rail).toBe('rail');
  });

  it('"[" toggles, but never with a modifier, while typing or while the palette is open', () => {
    render(1600);
    key('[');
    expect(side()?.dataset.rail).toBe('rail');
    key('[', { ctrlKey: true });
    key('[', { metaKey: true });
    key('[', { altKey: true });
    expect(side()?.dataset.rail).toBe('rail');
    const input = document.createElement('input');
    document.body.appendChild(input);
    key('[', {}, input);
    expect(side()?.dataset.rail).toBe('rail');
    unmount(component!); component = null; target.remove();
    localStorage.clear();
    render(1600, { blocked: true });
    key('[');
    expect(side()?.dataset.rail).toBe('open');
  });

  it('"[" opens the Menu drawer under 768px', () => {
    const menu = document.createElement('button');
    menu.id = 'nav-menu';
    let clicks = 0;
    menu.addEventListener('click', () => { clicks++; });
    document.body.appendChild(menu);
    render(600);
    key('[');
    expect(clicks).toBe(1);
  });
});

describe('Sidebar — the overlay sheet from 768 to 1199px', () => {
  function openSheet() {
    render(1024);
    toggle().click(); flushSync();
    expect(side()?.dataset.rail).toBe('over');
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    expect(localStorage.getItem('pacto-nav')).toBeNull(); // never stored
  }

  it('Escape closes it and returns focus to the button', () => {
    openSheet();
    (target.querySelector('a[data-nav="owners"]') as HTMLElement).focus();
    key('Escape');
    expect(side()?.dataset.rail).toBe('rail');
    expect(document.activeElement).toBe(toggle());
  });

  it('"[" moves focus into the sheet it opens', () => {
    render(1024);
    const outside = document.createElement('button');
    document.body.appendChild(outside);
    outside.focus();
    key('[');
    expect(side()?.dataset.rail).toBe('over');
    expect(document.activeElement).toBe(toggle());
    outside.remove();
  });

  it('leaves an Escape meant for a widget outside it alone', () => {
    openSheet();
    const outside = document.createElement('input');
    document.body.appendChild(outside);
    outside.focus();
    key('Escape', {}, outside);
    expect(side()?.dataset.rail).toBe('over');
    expect(document.activeElement).toBe(outside);
    outside.remove();
  });

  it('shows its rail tooltips on hover only where a real hover exists', async () => {
    const { readFileSync } = await import('node:fs');
    const css = readFileSync(`${process.cwd()}/src/components/Sidebar.svelte`, 'utf8'); // vitest runs from the package root
    expect(css).toMatch(/@media \(hover: none\) \{\s*\.side\[data-rail='rail'\] :global\(\.nav-item:hover:not\(:focus-visible\) \.nav-label\)/);
  });

  it('a click outside closes it, a click inside does not', () => {
    openSheet();
    side()!.dispatchEvent(new Event('pointerdown', { bubbles: true })); flushSync();
    expect(side()?.dataset.rail).toBe('over');
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true })); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
  });

  it('following a link closes it', () => {
    openSheet();
    (target.querySelector('a[data-nav="owners"]') as HTMLAnchorElement).click(); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
  });

  it('a route change closes it', () => {
    openSheet();
    window.dispatchEvent(new HashChangeEvent('hashchange')); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
  });

  it('leaving it with Tab closes it; moving inside it does not', () => {
    openSheet();
    const a = target.querySelector('a[data-nav="owners"]') as HTMLElement;
    side()!.dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: a })); flushSync();
    expect(side()?.dataset.rail).toBe('over');
    side()!.dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: document.body })); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
  });

  it('closes when the window leaves the band', () => {
    openSheet();
    setWidth(1300); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
    setWidth(1024); flushSync();
    expect(side()?.dataset.rail).toBe('rail');
  });
});

describe('Sidebar — items', () => {
  it('names each link by its label in both states, and marks the current one', () => {
    render(1280, { attentionTotal: 1234, sourceCounts: { total: 4, available: 3, unavailable: 1 } });
    const services = target.querySelector('a[data-nav="services"]') as HTMLElement;
    expect(services.getAttribute('aria-current')).toBe('page');
    expect(services.querySelector('.nav-label')?.textContent).toBe('Services');
    const att = target.querySelector('a[data-nav="attention"]') as HTMLElement;
    // The count is a description, so the name stays the label.
    expect(att.querySelector('.nav-cnt')?.textContent).toBe('1.2K');
    expect(att.querySelector('.nav-bdg')?.textContent).toBe('99+');
    expect(att.querySelector('.nav-cnt')?.getAttribute('aria-hidden')).toBe('true');
    expect(document.getElementById(att.getAttribute('aria-describedby')!)?.textContent).toBe('1,234 items to look at');
    const src = target.querySelector('a[data-nav="sources"]') as HTMLElement;
    expect(src.querySelector('.nav-dot')?.classList.contains('nav-part')).toBe(true);
    expect(document.getElementById(src.getAttribute('aria-describedby')!)?.textContent).toBe('3 of 4 sources reporting');
    unmount(component!); component = null; target.remove();
    // A stale or partial source still reports: the ring is put into words.
    render(1280, { sourceCounts: { total: 5, available: 5, stale: 2, partial: 1 } });
    const s2 = target.querySelector('a[data-nav="sources"]') as HTMLElement;
    expect(s2.querySelector('.nav-dot')?.classList.contains('nav-part')).toBe(true);
    expect(document.getElementById(s2.getAttribute('aria-describedby')!)?.textContent).toBe('5 of 5 sources reporting, 2 stale, 1 partial');
  });

  it('shows no badge and no dot while the counts are unknown', () => {
    render(1600);
    expect(target.querySelector('.nav-cnt')).toBeNull();
    expect(target.querySelector('.nav-dot')).toBeNull();
    expect(target.querySelector('a[data-nav="attention"]')?.hasAttribute('aria-describedby')).toBe(false);
  });

  it('hands each item its tooltip position on hover and focus', () => {
    render(1280);
    const a = target.querySelector('a[data-nav="owners"]') as HTMLElement;
    a.dispatchEvent(new Event('pointerover', { bubbles: true }));
    expect(a.style.getPropertyValue('--tip-x')).toMatch(/px$/);
    a.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    expect(a.style.getPropertyValue('--tip-y')).toMatch(/px$/);
    side()!.dispatchEvent(new Event('pointerover', { bubbles: true })); // not an item: nothing to place
  });
});
