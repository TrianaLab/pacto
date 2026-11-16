/**
 * Component tests for the external-system list: one row per canonical identity with its
 * class, its consumers and how it was joined, the class filter in the URL, the count of
 * systems only hidden services declare and the empty state.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetExternalsView from './FleetExternalsView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-11-16T10:00:00Z', completeness: 'complete', sources: [] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const listResp = (entities: any[], extra: any = {}): any => ({ meta, total: entities.length, count: entities.length, offset: 0, limit: 25, truncated: false, entities, ...extra });
const LONG = 'aws/rds/123456789012/eu-west-1/orders-db';

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetExternalsView, { target, props: { refreshTick: 0, ...props } });
  return { target, done: () => { unmount(component); document.body.removeChild(target); } };
}

beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

describe('FleetExternalsView', () => {
  it('lists each external system with its class, consumers and how it was joined', async () => {
    entitiesFn.mockResolvedValue(listResp([
      { kind: 'external', key: 'stripe', label: 'Stripe', secondary: 'vendor-api', status: 'unknown', consumers: 2, catalogEntry: 'stripe', href: '/fleet/externals/stripe' },
      { kind: 'external', key: LONG, label: LONG, secondary: 'managed-cloud', status: 'unknown', consumers: 1 },
    ]));
    const { target, done } = mountView({ klass: 'vendor-api', text: 'st' });
    await vi.waitFor(() => expect(target.querySelectorAll('[data-testid="external-list"] tbody tr').length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['external'], class: 'vendor-api', text: 'st', sort: 'consumers', limit: 25 }));
    expect(target.querySelector('h1')?.textContent).toBe('External systems');
    expect(Array.from(target.querySelectorAll('[data-testid="external-list"] thead th')).map((th) => th.textContent?.trim()))
      .toEqual(['Identity', 'Class', 'Consumers', 'Joined by']);
    const [stripe, rds] = Array.from(target.querySelectorAll('[data-testid="external-list"] tbody tr'));
    const cell = (row: Element, label: string) => row.querySelector(`[data-label="${label}"]`)!;
    expect(cell(stripe, 'Class').textContent).toBe('Vendor API');
    expect(cell(stripe, 'Consumers').textContent?.trim()).toBe('2');
    expect(cell(stripe, 'Joined by').textContent).toBe('catalog entry stripe');
    expect(cell(rds, 'Joined by').textContent).toBe('name only');
    expect(stripe.querySelector('a.entity-link')?.getAttribute('href')).toBe('#/fleet/externals/stripe');
    // A long identity keeps its full text on hover, inside <bdi>.
    const label = rds.querySelector('.ei-label');
    expect(label?.getAttribute('title')).toBe(LONG);
    expect(label?.querySelector('bdi')?.textContent).toBe(LONG);
    done();
  });

  it('writes the class filter into the URL', async () => {
    entitiesFn.mockResolvedValue(listResp([{ kind: 'external', key: 'stripe', label: 'Stripe', secondary: 'vendor-api', consumers: 1 }]));
    const { target, done } = mountView({ text: 'st' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="external-list"]')).not.toBeNull());
    const chips = Array.from(target.querySelectorAll('[data-testid="external-class-filter"] a'));
    expect(chips.map((a) => a.textContent)).toEqual(['All', 'Vendor API', 'Managed cloud', 'Legacy', 'Unclassified', 'Other egress']);
    expect(chips[1].getAttribute('href')).toBe('#/fleet/externals?text=st&class=vendor-api');
    expect(chips[0].getAttribute('href')).toBe('#/fleet/externals?text=st');
    expect(chips[0].getAttribute('aria-current')).toBe('true');
    done();
  });

  it('counts the external systems only hidden services declare, never naming them', async () => {
    entitiesFn.mockResolvedValue(listResp([{ kind: 'external', key: 'stripe', label: 'Stripe', secondary: 'vendor-api', consumers: 1 }], { hidden: 3 }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="external-hidden"]')).not.toBeNull());
    expect(target.querySelector('[data-testid="external-hidden"]')?.textContent).toBe('3 more external systems are used only by services you cannot see.');
    done();
  });

  it('says what puts a system here when no contract names one', async () => {
    entitiesFn.mockResolvedValue(listResp([]));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.textContent).toContain('No contract names an external system yet.'));
    done();
  });
});
