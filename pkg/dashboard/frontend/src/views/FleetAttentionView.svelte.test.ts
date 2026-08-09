/**
 * Component tests for FleetAttentionView.svelte — the paged attention list (A2/I).
 * Pagination is REAL: the view consumes the backend limit/offset/total/nextOffset and
 * keeps the page in the URL (Prev/Next are canonical hrefs), never slicing a preloaded
 * dataset. Covers first/next/last/previous pages, category+offset round-trip, category
 * reset dropping the offset, and the incomplete-knowledge caveat on every page. `api`
 * is mocked so only /api/fleet/attention is consumed.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { attentionFn } = vi.hoisted(() => ({ attentionFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetAttention: (...a: unknown[]) => attentionFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetAttentionView from './FleetAttentionView.svelte';

const PAGE = 25;
const TOTAL = 60; // three pages: 0-24, 25-49, 50-59

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function page(offset: number, partial = false): any {
  const start = Math.max(0, offset);
  const end = Math.min(TOTAL, start + PAGE);
  const items = [];
  for (let i = start; i < end; i++) {
    items.push({
      entity: { kind: 'target', key: `t${i}`, label: `t${i}`, href: `/fleet/targets/t${i}`, status: 'NonCompliant' },
      severity: 'warning', category: 'stale', summary: `item ${i}`,
    });
  }
  const nextOffset = end < TOTAL ? end : undefined;
  return {
    meta: {
      schemaVersion: 'pacto.dev/fleet-product/v1',
      completeness: partial ? 'partial' : 'complete',
      sources: partial ? [{ id: 'k8s', kind: 'k8s', status: 'unavailable' }] : [],
    },
    total: TOTAL, offset: start, limit: PAGE, count: items.length,
    truncated: nextOffset != null, nextOffset, items,
  };
}

// The mock answers the requested offset, so page facts are backend-driven.
function serveByOffset(partial = false) {
  attentionFn.mockImplementation((p: { offset?: number }) => Promise.resolve(page(p?.offset ?? 0, partial)));
}

function mountView(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetAttentionView, { target, props: { refreshTick: 0, ...props } });
  return { target, component };
}

const range = (t: HTMLElement) => t.querySelector('.av-range')?.textContent || '';
const prev = (t: HTMLElement) => t.querySelector('[data-testid="attn-prev"]') as HTMLAnchorElement | null;
const next = (t: HTMLElement) => t.querySelector('[data-testid="attn-next"]') as HTMLAnchorElement | null;

describe('FleetAttentionView — real backend pagination (A2)', () => {
  beforeEach(() => { attentionFn.mockReset(); location.hash = ''; });

  it('first page: renders 1-25 of 60, Prev disabled, Next -> offset 25', async () => {
    serveByOffset();
    const { target, component } = mountView({ category: '', offset: '' });
    await vi.waitFor(() => expect(target.querySelectorAll('.attn-item').length).toBe(25));
    expect(range(target)).toBe('Showing 1–25 of 60');
    expect(prev(target)).toBeNull(); // disabled span, not a link
    expect(next(target)?.getAttribute('href')).toBe('#/fleet/attention?offset=25');
    // page 1 requests no explicit offset (canonical page 1)
    expect(attentionFn).toHaveBeenCalledWith(expect.objectContaining({ limit: 25, offset: undefined }));
    unmount(component); document.body.removeChild(target);
  });

  it('next page: offset 25 renders 26-50 of 60 with Prev -> page 1 and Next -> offset 50', async () => {
    serveByOffset();
    const { target, component } = mountView({ category: '', offset: '25' });
    await vi.waitFor(() => expect(range(target)).toBe('Showing 26–50 of 60'));
    expect(attentionFn).toHaveBeenCalledWith(expect.objectContaining({ offset: 25, limit: 25 }));
    expect(prev(target)?.getAttribute('href')).toBe('#/fleet/attention'); // back to canonical page 1
    expect(next(target)?.getAttribute('href')).toBe('#/fleet/attention?offset=50');
    unmount(component); document.body.removeChild(target);
  });

  it('last page: offset 50 renders 51-60 of 60 with Next disabled', async () => {
    serveByOffset();
    const { target, component } = mountView({ category: '', offset: '50' });
    await vi.waitFor(() => expect(range(target)).toBe('Showing 51–60 of 60'));
    expect(target.querySelectorAll('.attn-item').length).toBe(10);
    expect(next(target)).toBeNull(); // disabled span
    expect(prev(target)?.getAttribute('href')).toBe('#/fleet/attention?offset=25');
    unmount(component); document.body.removeChild(target);
  });

  it('category + offset round-trip: both survive in the request and the Prev/Next hrefs', async () => {
    serveByOffset();
    const { target, component } = mountView({ category: 'stale', offset: '25' });
    await vi.waitFor(() => expect(range(target)).toBe('Showing 26–50 of 60'));
    expect(attentionFn).toHaveBeenCalledWith(expect.objectContaining({ category: 'stale', offset: 25 }));
    expect(next(target)?.getAttribute('href')).toBe('#/fleet/attention?category=stale&offset=50');
    expect(prev(target)?.getAttribute('href')).toBe('#/fleet/attention?category=stale'); // page 1, category kept
    unmount(component); document.body.removeChild(target);
  });

  it('changing the category resets the offset to page 1 (clearing drops the offset)', async () => {
    serveByOffset();
    const { target, component } = mountView({ category: 'stale', offset: '25' });
    await vi.waitFor(() => expect(target.querySelector('.chip')).toBeTruthy());
    (target.querySelector('.chip-x') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/attention'); // no category, no offset
    unmount(component); document.body.removeChild(target);
  });

  it('incomplete knowledge stays visible on a non-first page', async () => {
    serveByOffset(true);
    const { target, component } = mountView({ category: '', offset: '25' });
    await vi.waitFor(() => expect(range(target)).toBe('Showing 26–50 of 60'));
    expect(target.querySelector('.av-knowledge')).toBeTruthy(); // caveat shown on page 2
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetAttentionView — triage filters (I)', () => {
  beforeEach(() => { attentionFn.mockReset(); location.hash = ''; });

  it('the severity filter uses the backend param, lives in the URL and resets the page', async () => {
    serveByOffset();
    const { target, component } = mountView({ offset: '25' });
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    const sev = target.querySelector('select[aria-label="Filter by severity"]') as HTMLSelectElement;
    sev.value = 'error'; sev.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/attention?severity=error'); // offset reset to page 1
    unmount(component); document.body.removeChild(target);
  });

  it('combines category + severity in the request and shows a chip per active filter', async () => {
    serveByOffset();
    const { target, component } = mountView({ category: 'stale', severity: 'warning' });
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    expect(attentionFn).toHaveBeenCalledWith(expect.objectContaining({ category: 'stale', severity: 'warning' }));
    const chips = Array.from(target.querySelectorAll('.chip .chip-value')).map((c) => c.textContent);
    expect(chips).toEqual(expect.arrayContaining(['Stale evidence', 'warning']));
    unmount(component); document.body.removeChild(target);
  });

  it('the stale-only toggle carries into the URL and the request', async () => {
    serveByOffset();
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    const chk = target.querySelector('input[type="checkbox"]') as HTMLInputElement;
    chk.checked = true; chk.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/attention?staleOnly=1');
    unmount(component); document.body.removeChild(target);

    attentionFn.mockClear();
    const m2 = mountView({ staleOnly: '1' });
    await vi.waitFor(() => expect(attentionFn).toHaveBeenCalledWith(expect.objectContaining({ staleOnly: true })));
    unmount(m2.component); document.body.removeChild(m2.target);
  });

  it('an item answers what/why/severity/source/nextStep', async () => {
    attentionFn.mockResolvedValue({
      meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources: [] },
      total: 1, offset: 0, limit: 25, count: 1, truncated: false, nextOffset: undefined,
      items: [{ severity: 'error', category: 'non-compliant', entity: { kind: 'target', key: 'prod/k8s/a', label: 'a', href: '/fleet/targets/prod%2Fk8s%2Fa' }, summary: 'confirmed drift', source: 'kubernetes', nextStep: 'inspect the deployment' }],
    });
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).toContain('confirmed drift');       // why
    expect(text).toContain('via kubernetes');         // evidence source
    expect(text).toContain('inspect the deployment'); // backend-provided nextStep
    unmount(component); document.body.removeChild(target);
  });
});
