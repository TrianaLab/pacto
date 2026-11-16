/**
 * GroupTable: the Owners and Domains tables. Each row counts its services by
 * compliance, health and knowledge; every count column but Rate is a server sort, and
 * one explicit link ranks the rows by health.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import GroupTable from './GroupTable.svelte';

const row = { key: 'team:pay', label: 'pay', kind: 'owner', href: '/fleet/owners/team:pay', services: 9, owned: 8, targets: 12, nonCompliant: 2, unknown: 3, failingHealth: 1, stale: 4, withGaps: 5 };
const sortHref = (s: string) => `#/sort/${s}`;
const statusHref = (r: { key: string }, status: string) => `#/svc/${r.key}/${status}`;

function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(GroupTable, { target, props: { entities: [row], order: 'nonCompliant', sortHref, statusHref, testid: 'gt', ...props } });
  return { target, done: () => { unmount(component); document.body.removeChild(target); } };
}
const heads = (t: HTMLElement) => Array.from(t.querySelectorAll('thead th')).map((th) => th.textContent?.replace(/[▲▼]/g, '').trim());
const labels = (t: HTMLElement) => Array.from(t.querySelectorAll('tbody td')).map((td) => td.getAttribute('data-label'));
const cells = (t: HTMLElement) => Array.from(t.querySelectorAll('tbody td')).map((td) => td.textContent?.trim());

describe('GroupTable', () => {
  it('counts an owner row by compliance, health and knowledge', () => {
    const { target, done } = render({ kind: 'owner' });
    expect(heads(target)).toEqual(['Owner', 'Services', 'Non-compliant', 'Unknown', 'Failing', 'Stale', 'Gaps', 'Rate']);
    expect(labels(target)).toEqual(['Services', 'Non-compliant', 'Unknown', 'Failing', 'Stale', 'Gaps', 'Rate']);
    expect(cells(target)).toEqual(['9', '2', '3', '1', '4', '5', '25%']);
    const links = Array.from(target.querySelectorAll('tbody td a')).map((a) => a.getAttribute('href'));
    expect(links).toEqual(['#/svc/team:pay/NonCompliant', '#/svc/team:pay/Unknown']);
    done();
  });

  it('adds the Targets column on a domain row', () => {
    const { target, done } = render({ kind: 'domain', entities: [{ ...row, kind: 'domain', key: 'commerce' }] });
    expect(heads(target)).toEqual(['Domain', 'Services', 'Targets', 'Non-compliant', 'Unknown', 'Failing', 'Stale', 'Gaps', 'Rate']);
    expect(labels(target)).toContain('Targets');
    done();
  });

  it('marks the active sort, gaps included, and sorts gaps most first', () => {
    const { target, done } = render({ kind: 'owner', order: 'gaps' });
    const gaps = Array.from(target.querySelectorAll('thead th')).find((th) => th.textContent?.includes('Gaps')) as HTMLElement;
    expect(gaps.getAttribute('aria-sort')).toBe('descending');
    expect(gaps.querySelector('a')?.getAttribute('href')).toBe('#/sort/gaps');
    const nc = Array.from(target.querySelectorAll('thead th')).find((th) => th.textContent?.includes('Non-compliant')) as HTMLElement;
    expect(nc.getAttribute('aria-sort')).toBe('none');
    done();
  });

  it('offers one Rank by health link, current only when the rows rank by health', () => {
    let r = render({ kind: 'domain' });
    let link = r.target.querySelector('[data-testid="gt-rank-health"]') as HTMLAnchorElement;
    expect(link.textContent?.trim()).toBe('Rank by health');
    expect(link.getAttribute('href')).toBe('#/sort/health');
    expect(link.hasAttribute('aria-current')).toBe(false);
    r.done();
    r = render({ kind: 'domain', order: 'health' });
    link = r.target.querySelector('[data-testid="gt-rank-health"]') as HTMLAnchorElement;
    expect(link.getAttribute('aria-current')).toBe('true');
    r.done();
  });

  it('turns into cards in a narrow frame, with a ch floor on every column', () => {
    const { target, done } = render({ kind: 'domain' });
    const frame = target.querySelector('table')?.parentElement as HTMLElement;
    expect(frame.classList.contains('cq-cards')).toBe(true);
    expect(frame.classList.contains('cq-cards-xl')).toBe(true);
    const cols = Array.from(target.querySelectorAll('col')).map((c) => c.getAttribute('style') || '');
    expect(cols.length).toBe(9);
    expect(cols.slice(1).every((w) => /ch/.test(w))).toBe(true);
    done();
  });
});
