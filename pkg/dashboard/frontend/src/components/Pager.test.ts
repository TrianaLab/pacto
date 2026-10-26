import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync, createRawSnippet } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import Pager from './Pager.svelte';
// @ts-expect-error — Svelte components have no declaration files
import CappedList from './CappedList.svelte';
// @ts-expect-error — Svelte components have no declaration files
import ListFilter from './ListFilter.svelte';

let app: ReturnType<typeof mount> | null = null;
afterEach(() => { if (app) unmount(app); app = null; document.body.innerHTML = ''; location.hash = ''; });

const href = (off: number, lim: number) => `#/x?offset=${off}&limit=${lim}`;
const render = (C: unknown, props: Record<string, unknown>) => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- component handle
  app = mount(C as any, { target: document.body, props });
  flushSync();
  return document.body;
};
const words = (el: Element | null) => el?.textContent?.replace(/\s+/g, ' ').trim();

describe('Pager', () => {
  it('shows the range with grouped thousands and links to every move', () => {
    const b = render(Pager, { page: { total: 12431, offset: 50, limit: 50 }, href, noun: 'changes' });
    expect(words(b.querySelector('[data-testid="pager-range"]'))).toBe('Showing 51–100 of 12,431 changes');
    const link = (rel: string) => b.querySelector(`[data-testid="pager-${rel}"]`)?.getAttribute('href');
    expect([link('first'), link('prev'), link('next'), link('last')]).toEqual([href(0, 50), href(0, 50), href(100, 50), href(12400, 50)]);
    expect(b.textContent).toContain('of 249');
  });

  it('greys out what leads nowhere and hides the moves on one page', () => {
    let b = render(Pager, { page: { total: 120, offset: 0, limit: 50 }, href });
    expect([...b.querySelectorAll('.pl-page.disabled')].map((e) => e.textContent)).toEqual(['First', 'Previous']);
    unmount(app!); app = null; document.body.innerHTML = '';
    b = render(Pager, { page: { total: 3, offset: 0, limit: 25 }, href, noun: 'environments' });
    expect(words(b.querySelector('[data-testid="pager-range"]'))).toBe('3 environments');
    expect(b.querySelector('.pl-pager-btns')).toBeNull();
    expect(b.querySelector('[data-testid="pager-size"]')).toBeNull();
    unmount(app!); app = null; document.body.innerHTML = '';
    b = render(Pager, { page: { total: 0, offset: 0, limit: 25 }, href });
    expect(b.querySelector('nav')).toBeNull();
  });

  it('jumps to a page and changes the page size through the URL', () => {
    const b = render(Pager, { page: { total: 500, offset: 0, limit: 25 }, href });
    const input = b.querySelector('[data-testid="pager-jump"]') as HTMLInputElement;
    input.value = '7';
    input.dispatchEvent(new Event('input'));
    (input.form as HTMLFormElement).requestSubmit();
    expect(location.hash).toBe(href(150, 25));
    const size = b.querySelector('[data-testid="pager-size"]') as HTMLSelectElement;
    expect([...size.options].map((o) => o.value)).toEqual(['25', '50', '100']);
    size.value = '100';
    size.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe(href(0, 100));
  });
});

describe('CappedList', () => {
  const item = createRawSnippet((x: () => string) => ({ render: () => `<span>${x()}</span>` }));

  it('shows the first few, then all of them on request', () => {
    const b = render(CappedList, { items: ['a', 'b', 'c', 'd'], max: 2, item, inline: true });
    expect([...b.querySelectorAll('li')].map((l) => l.textContent)).toEqual(['a', 'b']);
    const more = b.querySelector('button') as HTMLButtonElement;
    expect(more.textContent?.trim()).toBe('Show all 4');
    more.click();
    flushSync();
    expect(b.querySelectorAll('li').length).toBe(4);
    expect(more.getAttribute('aria-expanded')).toBe('true');
    expect(more.textContent?.trim()).toBe('Show fewer');
  });

  it('draws nothing for nothing, and no button when all fit', () => {
    let b = render(CappedList, { items: [], item });
    expect(b.querySelector('ul')).toBeNull();
    unmount(app!); app = null; document.body.innerHTML = '';
    b = render(CappedList, { items: ['a'], item });
    expect(b.querySelector('button')).toBeNull();
  });
});

describe('ListFilter', () => {
  it('commits the filter to the URL on submit and offers to clear it', () => {
    const b = render(ListFilter, { value: 'pay', href: (q: string) => `#/x?q=${q}`, label: 'Filter' });
    const input = b.querySelector('input') as HTMLInputElement;
    expect(input.value).toBe('pay');
    input.value = '  carol ';
    input.dispatchEvent(new Event('input'));
    (b.querySelector('form') as HTMLFormElement).requestSubmit();
    expect(location.hash).toBe('#/x?q=carol');
    expect(b.querySelector('a')?.getAttribute('href')).toBe('#/x?q=');
  });
});
