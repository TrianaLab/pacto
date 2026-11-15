/**
 * The Changes section of a service page (#459): every declared change in version
 * order, a time only from a delivery record read as the caller in a second request,
 * and every line without one saying why.
 */
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { detailFn } = vi.hoisted(() => ({ detailFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import ServiceChanges from './ServiceChanges.svelte';

const rev = (version: string) => ({ kind: 'revision', key: `web@${version}`, label: `web ${version}`, version, href: `/fleet/revisions/web%40${version}` });
const ev = (over: Record<string, unknown> = {}) => ({ kind: 'owner', change: 'changed', from: 'team:a', to: 'team:b', revision: rev('1.1.0'), previous: rev('1.0.0'), provenance: 'declared', source: 'contract', ...over });
const changes = (items: unknown[], over: Record<string, unknown> = {}) => ({ total: items.length, count: items.length, truncated: false, items, gaps: 0, completeness: 'complete', times: 'not-read', ...over });
const reach = (over: Record<string, unknown> = {}) => ({ namespace: 'prod-ns', stage: 'web', environment: 'prod', at: '2026-11-10T14:02:00Z', record: 'rec-1', verified: true, ...over });

let cleanup: (() => void) | null = null;
beforeEach(() => { detailFn.mockReset(); });
afterEach(() => { cleanup?.(); cleanup = null; });
function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ServiceChanges, { target, props: { service: 'web', label: 'web', ...props } });
  flushSync();
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}
const text = (el: Element | null | undefined) => el?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
const caption = (t: Element) => text(t.querySelector('[data-testid="service-changes-caption"]'));
const rows = (t: Element) => [...t.querySelectorAll('[data-testid="service-change-row"]')];
const reached = (t: Element, i = 0) => text(rows(t)[i]?.querySelector('[data-testid="service-change-reached"]'));

describe('ServiceChanges', () => {
  it('generates its caption from the answer', () => {
    const t = render({ changes: changes([ev(), ev({ kind: 'environment', subject: 'dev', change: 'present', previous: undefined, from: '', to: '' })], { completeness: 'partial', completenessReason: 'you may not see every revision' }) });
    expect(caption(t)).toBe('2 changes across the versions Pacto holds, in version order; partial: you may not see every revision; times not recorded: no delivery record was read');
  });

  it('filters by kind through links with counts', () => {
    const items = [ev(), ev({ kind: 'environment', subject: 'dev', change: 'added' }), ev({ kind: 'environment', subject: 'qa', change: 'removed' })];
    const t = render({ changes: changes(items) });
    const chips = [...t.querySelectorAll('[data-testid="service-changes-kind"]')];
    expect(chips.map(text)).toEqual(['owner 1', 'environment 2']);
    expect(chips[1].getAttribute('href')).toBe('#/fleet/services/web?changesKind=environment&section=changes');
    cleanup?.();
    const f = render({ changes: changes(items), kind: 'environment' });
    expect(rows(f)).toHaveLength(2);
    expect(text(f.querySelector('[data-testid="service-changes-filtered"]'))).toBe('2 changes of kind environment');
  });

  it('reads each reached state honestly', () => {
    const items = [ev({ reached: [reach()] }), ev({ reached: [reach({ verified: false })] }), ev({ reached: [reach({ record: '', fromStatus: true, verified: false })] }), ev()];
    const t = render({ changes: changes(items, { times: 'read' }) });
    expect(reached(t, 0)).toBe('prod 2026-11-10 14:02 UTC');
    expect(rows(t)[0].querySelector('[data-testid="service-change-reached"] a')?.getAttribute('href')).toBe('#/delivery/history?namespace=prod-ns&stage=web');
    expect(reached(t, 1)).toContain('recorded, signature not checked');
    expect(rows(t)[1].querySelector('[data-testid="service-change-reached"] a')).toBeNull();
    expect(reached(t, 2)).toContain('from status, not signed');
    expect(reached(t, 3)).toBe('time not recorded');
    expect(caption(t)).toContain('times from delivery records');
    cleanup?.();
    const p = render({ changes: changes([ev()], { times: 'partial', timesUnreadable: ['team-a', 'team-b'] }) });
    expect(reached(p)).toBe('time not recorded in the Stages read');
    expect(caption(p)).toContain('times from the Stages read; team-a and team-b could not be read');
    cleanup?.();
    const h = render({ changes: changes([ev()], { times: 'hidden', timesReason: 'you may not list Stages' }) });
    expect(reached(h)).toBe('time hidden');
    expect(caption(h)).toContain('you may not list Stages');
  });

  it('asks for reached times only when the dashboard serves delivery', async () => {
    const t = render({ changes: changes([ev()]), deliveryState: 'disabled' });
    expect(detailFn).not.toHaveBeenCalled();
    expect(reached(t)).toBe('time not recorded');
    cleanup?.();
    let resolve: (v: unknown) => void = () => {};
    detailFn.mockReturnValue(new Promise((r) => { resolve = r; }));
    const e = render({ changes: changes([ev()]), deliveryState: 'enabled' });
    expect(detailFn).toHaveBeenCalledWith('service', 'web', { reached: true });
    expect(reached(e)).toBe('reading times from delivery records');
    resolve({ service: { changes: changes([ev({ reached: [reach()] })], { times: 'read' }) } });
    await vi.waitFor(() => expect(reached(e)).toBe('prod 2026-11-10 14:02 UTC'));
  });

  it('keeps the fleet answer when the reached read fails', async () => {
    detailFn.mockRejectedValue(new Error('delivery unavailable'));
    const t = render({ changes: changes([ev()]), deliveryState: 'enabled' });
    await vi.waitFor(() => expect(caption(t)).toContain('times not read: delivery unavailable'));
    expect(rows(t)).toHaveLength(1);
    expect(reached(t)).toBe('time not recorded');
  });

  it('links each row with a previous revision to Change analysis with a distinct label', () => {
    const t = render({ changes: changes([ev(), ev({ revision: rev('1.2.0'), previous: rev('1.1.0') }), ev({ change: 'present', previous: undefined, from: '', to: 'team:a', revision: rev('1.0.0') })]) });
    const links = [...t.querySelectorAll('a.sc-analyse')];
    expect(links).toHaveLength(2);
    expect(links[0].getAttribute('href')).toBe('#/fleet/changes/web?old=web%401.0.0&new=web%401.1.0');
    expect(links.map((a) => a.getAttribute('aria-label'))).toEqual([
      "Analyse 1.0.0 to 1.1.0 against today's graph", "Analyse 1.1.0 to 1.2.0 against today's graph"]);
    expect(rows(t)[2].querySelector('a.sc-analyse')).toBeNull();
  });

  it('wraps the table in a labelled, focusable region with labelled narrow cells', () => {
    const long = 'team:' + 'settlement-reconciliation-'.repeat(8);
    const t = render({ changes: changes([ev({ to: long })]) });
    const region = t.querySelector('[role="region"]')!;
    expect(region.getAttribute('aria-label')).toBe('Changes of web');
    expect(region.getAttribute('tabindex')).toBe('0');
    expect([...rows(t)[0].querySelectorAll('[role="cell"]')].map((c) => c.getAttribute('data-label'))).toEqual(['Version', 'Change', 'From → To', 'Reached', 'Source']);
    expect(rows(t)[0].querySelector(`bdi[title="${long}"]`)).not.toBeNull();
  });

  it('pages a long preview through links and states gaps', () => {
    const items = Array.from({ length: 30 }, (_, i) => ev({ revision: rev(`1.${i + 1}.0`), previous: rev(`1.${i}.0`) }));
    const t = render({ changes: changes(items, { gaps: 2 }) });
    expect(rows(t)).toHaveLength(25);
    expect(t.querySelector('[data-testid="changes-pager-next"]')?.getAttribute('href')).toBe('#/fleet/services/web?changesOffset=25&section=changes');
    expect(text(t)).toContain('2 revisions could not be read; changes across them are unknown');
    cleanup?.();
    expect(rows(render({ changes: changes(items), offset: '25' }))).toHaveLength(5);
  });

  it('labels the source of each change', () => {
    const t = render({ changes: changes([ev(), ev({ kind: 'finding', subject: 'MISSING_READINESS', change: 'added', source: 'validation', from: '', to: '' })]) });
    expect(text(rows(t)[1])).toContain('MISSING_READINESS appeared');
    expect(text(rows(t)[1].querySelector('[data-label="Source"]'))).toBe('validation');
    expect(text(rows(t)[0].querySelector('[data-label="Source"]'))).toBe('declared');
  });
});
