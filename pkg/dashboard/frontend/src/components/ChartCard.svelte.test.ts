/**
 * The Metrics chart card (#597): a titled figure with its question, a "Show as table"
 * toggle and a table view built from lib/metrics.ts tableRows, paged locally.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount, flushSync, createRawSnippet } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ChartCard from './ChartCard.svelte';

const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();
const chart = createRawSnippet(() => ({ render: () => '<div data-testid="the-chart">chart</div>' }));
const foot = createRawSnippet(() => ({ render: () => '<p data-testid="the-foot">key</p>' }));
const rows = (n: number) => Array.from({ length: n }, (_, i) => [{ text: `env-${i}`, title: `env-${i}` }, { text: String(i) }]);

function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ChartCard, { target, props: { title: 'Lead time by environment', question: 'Publish to promotion', chart, ...props } });
  flushSync();
  return { target, done: () => { unmount(c); target.remove(); } };
}

describe('ChartCard', () => {
  it('is a figure section with its title, question, chart and foot', () => {
    const { target, done } = render({ foot, tables: [{ caption: 'c', head: ['Environment', 'p50'], rows: rows(2) }] });
    const root = target.querySelector('section') as HTMLElement;
    expect(root.hasAttribute('data-fig')).toBe(true);
    expect(root.querySelector('h2')?.textContent).toBe('Lead time by environment');
    expect(root.getAttribute('aria-labelledby')).toBe(root.querySelector('h2')?.id);
    expect(text(root)).toContain('Publish to promotion');
    expect(root.querySelector('[data-testid="the-chart"]')).not.toBeNull();
    expect(root.querySelector('[data-testid="the-foot"]')).not.toBeNull();
    expect(root.querySelector('table')).toBeNull();
    done();
  });

  it('flips to the table and back, saying which it shows', () => {
    const { target, done } = render({ tables: [{ caption: 'Lead per environment', head: ['Environment', 'p50'], rows: [[{ text: 'prod', title: 'prod' }, { text: 'unknown', unknown: true }]] }] });
    const btn = target.querySelector('button') as HTMLButtonElement;
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    expect(btn.textContent?.trim()).toBe('Show as table');
    btn.click(); flushSync();
    expect(btn.getAttribute('aria-pressed')).toBe('true');
    expect(btn.textContent?.trim()).toBe('Show as chart');
    expect(target.querySelector('[data-testid="the-chart"]')).toBeNull();
    const table = target.querySelector('table') as HTMLTableElement;
    expect(text(table.querySelector('caption'))).toBe('Lead per environment');
    expect(Array.from(table.querySelectorAll('thead th')).map((th) => [th.textContent, th.getAttribute('scope')])).toEqual([['Environment', 'col'], ['p50', 'col']]);
    const th = table.querySelector('tbody th') as HTMLElement;
    expect([th.getAttribute('scope'), th.getAttribute('title'), text(th)]).toEqual(['row', 'prod', 'prod']);
    const td = table.querySelector('tbody td') as HTMLElement;
    expect(td.getAttribute('data-label')).toBe('p50');
    expect(td.textContent).toBe('unknown');
    expect(td.classList.contains('cc-unk')).toBe(true);
    btn.click(); flushSync();
    expect(target.querySelector('table')).toBeNull();
    expect(target.querySelector('[data-testid="the-chart"]')).not.toBeNull();
    done();
  });

  it('links a count to its records with how many it lists', () => {
    const { target, done } = render({ tables: [{ caption: 'c', head: ['Environment', 'Held'], rows: [[{ text: 'prod' }, { text: '1/5 (20%)', href: '#/metrics?metric=held&environment=prod', lists: 1 }]] }] });
    (target.querySelector('button') as HTMLButtonElement).click(); flushSync();
    const a = target.querySelector('tbody td a') as HTMLAnchorElement;
    expect([a.getAttribute('href'), a.getAttribute('data-lists'), a.textContent]).toEqual(['#/metrics?metric=held&environment=prod', '1', '1/5 (20%)']);
    done();
  });

  it('pages a long table locally at 10, never with the URL pager', () => {
    const { target, done } = render({ tables: [{ caption: 'c', head: ['Environment', 'n'], rows: rows(23) }] });
    (target.querySelector('button') as HTMLButtonElement).click(); flushSync();
    const names = () => Array.from(target.querySelectorAll('tbody th')).map((th) => th.textContent);
    expect(names()).toHaveLength(10);
    expect(names()[0]).toBe('env-0');
    expect(text(target.querySelector('.cc-pager'))).toContain('1 to 10 of 23');
    const btn = (label: string) => Array.from(target.querySelectorAll('button')).find((b) => b.textContent?.trim().startsWith(label)) as HTMLButtonElement;
    expect(btn('Previous').disabled).toBe(true);
    btn('Next').click(); flushSync();
    expect(names()[0]).toBe('env-10');
    btn('Next').click(); flushSync();
    expect(names()).toHaveLength(3);
    expect(btn('Next').disabled).toBe(true);
    btn('Show all 23').click(); flushSync();
    expect(names()).toHaveLength(23);
    expect(target.querySelector('[data-view-total]')).toBeNull();
    done();
  });

  it('clamps the page when the rows under it shrink, and shows the chart when none are left', () => {
    const props = $state({ title: 't', chart, tables: [{ caption: 'c', head: ['Environment', 'n'], rows: rows(200) }] });
    const target = document.createElement('div');
    document.body.appendChild(target);
    const c = mount(ChartCard, { target, props });
    flushSync();
    (target.querySelector('button') as HTMLButtonElement).click(); flushSync();
    const next = () => Array.from(target.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Next') as HTMLButtonElement;
    for (let i = 0; i < 15; i++) { next().click(); flushSync(); }
    expect(text(target.querySelector('.cc-pager'))).toContain('151 to 160 of 200');
    props.tables = [{ caption: 'c', head: ['Environment', 'n'], rows: rows(20) }]; flushSync();
    expect(target.querySelectorAll('tbody th')).toHaveLength(10);
    expect(text(target.querySelector('.cc-pager'))).toContain('11 to 20 of 20');
    props.tables = [{ caption: 'c', head: ['Environment', 'n'], rows: rows(4) }]; flushSync();
    expect(target.querySelectorAll('tbody th')).toHaveLength(4);
    props.tables = []; flushSync();
    expect(target.querySelector('[data-testid="the-chart"]')).not.toBeNull();
    unmount(c); target.remove();
  });

  it('draws no toggle when there is no table to show', () => {
    const { target, done } = render({ tables: [] });
    expect(target.querySelector('button')).toBeNull();
    done();
  });
});
