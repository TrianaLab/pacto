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
    // The shared KnowledgeBanner, not a per-view copy: one sentence, one style, one
    // class, so a caveat cannot read differently here than on any other product screen.
    expect(target.querySelector('.knowledge')?.textContent).toMatch(/this attention list may be incomplete/i);
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
    // The chip reads the severity word, not the wire token: the URL carries `warning`,
    // the user reads "Warning" — the same word the row badge next to it uses.
    expect(chips).toEqual(expect.arrayContaining(['Stale evidence', 'Warning']));
    unmount(component); document.body.removeChild(target);
  });

  it('grades severity in its own vocabulary, not the compliance one', async () => {
    // Severity went through the compliance badge, which had no case for it: an `error`
    // row printed a grey lowercase "error" — the loudest fact on the triage screen
    // rendered as the quietest thing on it.
    attentionFn.mockImplementation(() => Promise.resolve({
      ...page(0), total: 1, count: 1, truncated: false, nextOffset: undefined,
      items: [{
        entity: { kind: 'target', key: 't0', label: 't0', href: '/fleet/targets/t0', status: 'NonCompliant' },
        severity: 'error', category: 'non-compliant', summary: 'confirmed drift',
      }],
    }));
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    const sev = target.querySelector('.attn-item .tag') as HTMLElement;
    expect(sev.textContent).toBe('Error');
    expect(sev.className).toContain('tone-err'); // red, not neutral grey
    expect(target.textContent).not.toContain('error'); // never the raw wire token
    unmount(component); document.body.removeChild(target);
  });

  it('names a compliance status in the picker the same way the badges do', async () => {
    serveByOffset();
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    const opts = Array.from(target.querySelectorAll('select[aria-label="Filter by compliance status"] option'));
    const texts = opts.map((o) => o.textContent);
    expect(texts).toContain('Not compliant');
    expect(texts).not.toContain('NonCompliant'); // wire enum stays the VALUE
    expect((opts.find((o) => o.textContent === 'Not compliant') as HTMLOptionElement).value).toBe('NonCompliant');
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

/**
 * An owner page's "View all for this owner" and its posture bars are CANONICAL owner
 * actions: they carry `ownerKey`, matched exactly, so the backlog of `team-a` cannot
 * quietly include `team-a-platform`'s items. The advanced Owner box stays a search.
 */
describe('FleetAttentionView — owner search vs owner identity', () => {
  beforeEach(() => { attentionFn.mockReset(); location.hash = ''; });

  it('asks the backend the exact owner question a canonical owner link carried', async () => {
    serveByOffset();
    const { target, component } = mountView({ ownerKey: 'team-a' });
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    expect(attentionFn).toHaveBeenCalledWith(expect.objectContaining({ ownerKey: 'team-a' }));
    expect(attentionFn).toHaveBeenCalledWith(expect.not.objectContaining({ owner: 'team-a' }));
    // The filter in force is legible, and named as the exact one.
    expect(Array.from(target.querySelectorAll('.chip')).map((c) => c.textContent?.replace(/\s+/g, ' ').trim()))
      .toEqual(['Owner: team-a ×']);
    unmount(component); document.body.removeChild(target);
  });

  it('typing in the Owner box widens back to a search, replacing the exact owner', async () => {
    serveByOffset();
    const { target, component } = mountView({ ownerKey: 'team-a' });
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    const box = target.querySelector('input[aria-label="Filter by owner"]') as HTMLInputElement;
    expect(box.value).toBe('team-a');
    box.value = 'team'; box.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/attention?owner=team');
    unmount(component); document.body.removeChild(target);
  });
});

/**
 * The RATION (styles/tokens.css). Motion on this page is spent on severity and nothing
 * else, so the number of moving rows is the number of rows worth looking at. These tests
 * hold the two ways that promise breaks: a row that moves when it should not, and a
 * second thing ringing at the same time as the first.
 */
describe('FleetAttentionView — the motion ration', () => {
  beforeEach(() => { attentionFn.mockReset(); location.hash = ''; });

  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
  function serve(items: any[], severities?: Record<string, number>) {
    attentionFn.mockResolvedValue({
      meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources: [] },
      total: items.length, offset: 0, limit: 25, count: items.length, truncated: false,
      severities, items,
    });
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
  const item = (severity: string, key: string, over: Record<string, unknown> = {}): any => ({
    severity, category: 'stale', code: 'STALE_EVIDENCE', label: `l-${key}`, summary: `s-${key}`,
    entity: { kind: 'target', key, label: key, href: `/fleet/targets/${key}` }, ...over,
  });

  it('rings exactly one row, and only when the worst thing on the page is an error', async () => {
    serve([item('error', 'a'), item('error', 'b'), item('warning', 'c')]);
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelectorAll('.attn-item').length).toBe(3));
    const rung = target.querySelectorAll('.attn-item.is-alarm');
    expect(rung).toHaveLength(1);
    // The head of the page, because the backend sorts errors first -- so the ring is on
    // the worst thing visible rather than on whichever row happened to render first.
    expect(target.querySelectorAll('.attn-item')[0].classList.contains('is-alarm')).toBe(true);
    unmount(component); document.body.removeChild(target);
  });

  it('does not ring when nothing on the page is an error', async () => {
    serve([item('warning', 'a'), item('info', 'b')]);
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelectorAll('.attn-item').length).toBe(2));
    expect(target.querySelectorAll('.attn-item.is-alarm')).toHaveLength(0);
    unmount(component); document.body.removeChild(target);
  });

  it('carries the severity on the row, so the rail is not a second source of truth', async () => {
    serve([item('error', 'a'), item('warning', 'b'), item('info', 'c')]);
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelectorAll('.attn-item').length).toBe(3));
    expect(Array.from(target.querySelectorAll('.attn-item')).map((r) => r.className.match(/sev-\w+/)?.[0]))
      .toEqual(['sev-error', 'sev-warning', 'sev-info']);
    unmount(component); document.body.removeChild(target);
  });

  it('renders two items that share a code and an entity, instead of throwing on a duplicate key', async () => {
    // Real payload shape: one target, one finding code, two revisions of it. Keyed on
    // code+entity alone this is a duplicate key, which in Svelte is a thrown error and a
    // blank page -- not a warning.
    serve([
      item('error', 'prod/k8s/a', { label: 'revision 1 is not compliant' }),
      item('error', 'prod/k8s/a', { label: 'revision 2 is not compliant' }),
    ]);
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelectorAll('.attn-item').length).toBe(2));
    unmount(component); document.body.removeChild(target);
  });

  it('says what the order means in the header', async () => {
    serve([item('error', 'a')], { errors: 1, warnings: 0, infos: 0 });
    const { target, component } = mountView({});
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    expect(target.querySelector('[data-testid="page-title"]')?.parentElement?.textContent)
      .toContain('1 item · most urgent first');
    unmount(component); document.body.removeChild(target);

    serve([item('info', 'a')], { errors: 0, warnings: 0, infos: 1 });
    const m2 = mountView({});
    await vi.waitFor(() => expect(m2.target.querySelector('.attn-item')).toBeTruthy());
    expect(m2.target.querySelector('[data-testid="page-title"]')?.parentElement?.textContent)
      .toContain('1 item · nothing urgent');
    unmount(m2.component); document.body.removeChild(m2.target);
  });
});
