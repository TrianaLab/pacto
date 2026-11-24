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
      { kind: 'external', key: LONG, label: LONG, secondary: 'managed-cloud', status: 'unknown', consumers: 1, joinedBy: 'normalized-name' },
      { kind: 'external', key: 'aws/rds/1/eu-west-1/orders', label: 'orders-db', secondary: 'unclassified', status: 'unknown', consumers: 2, joinedBy: 'identity' },
      { kind: 'external', key: 'host/example.com', label: 'api.example.com', secondary: 'unclassified', status: 'unknown', consumers: 2, joinedBy: 'host' },
    ]));
    const { target, done } = mountView({ klass: 'vendor-api', text: 'st' });
    await vi.waitFor(() => expect(target.querySelectorAll('[data-testid="external-list"] tbody tr').length).toBe(4));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['external'], class: 'vendor-api', text: 'st', sort: 'consumers', limit: 25 }));
    expect(target.querySelector('h1')?.textContent).toBe('External systems');
    expect(Array.from(target.querySelectorAll('[data-testid="external-list"] thead th')).map((th) => th.textContent?.trim()))
      .toEqual(['Identity', 'Class', 'Consumers', 'Joined by']);
    const [stripe, rds, byIdentity, byHost] = Array.from(target.querySelectorAll('[data-testid="external-list"] tbody tr'));
    const cell = (row: Element, label: string) => row.querySelector(`[data-label="${label}"]`)!;
    expect(cell(stripe, 'Class').textContent).toBe('Vendor API');
    expect(cell(stripe, 'Consumers').textContent?.trim()).toBe('2');
    expect(cell(stripe, 'Joined by').textContent).toBe('catalog entry stripe');
    expect(cell(rds, 'Joined by').textContent).toBe('name only');
    // Only a normalized-name join is "name only".
    expect(cell(byIdentity, 'Joined by').textContent).toBe('declared identity');
    expect(cell(byHost, 'Joined by').textContent).toBe('registrable domain');
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
    const chips = Array.from(target.querySelectorAll('[data-testid="external-class-filter"] a[data-class]'));
    expect(chips.map((a) => a.textContent)).toEqual(['All', 'Vendor API', 'Managed cloud', 'Legacy', 'Unclassified', 'Other egress']);
    expect(chips[1].getAttribute('href')).toBe('#/fleet/externals?text=st&class=vendor-api');
    expect(chips[0].getAttribute('href')).toBe('#/fleet/externals?text=st');
    expect(chips[0].getAttribute('aria-current')).toBe('true');
    done();
  });

  it('outside contract chip filters', async () => {
    entitiesFn.mockResolvedValue(listResp([{ kind: 'external', key: 'aws/rds', label: 'Amazon RDS', secondary: 'managed-cloud', consumers: 1 }]));
    const { target, done } = mountView({ klass: 'managed-cloud' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="external-list"]')).not.toBeNull());
    expect(entitiesFn).toHaveBeenLastCalledWith(expect.not.objectContaining({ outsideContract: true }));
    const chip = target.querySelector('[data-testid="external-outside-contract"]')!;
    // One chip after the class chips, in the same row, in its own navigation (it is no
    // class), keeping the class.
    const nav = chip.parentElement!;
    expect(nav.getAttribute('data-testid')).toBe('external-contract-filter');
    expect(nav.getAttribute('aria-label')).toBe('Filter by contract');
    expect(nav.previousElementSibling?.getAttribute('data-testid')).toBe('external-class-filter');
    expect(target.querySelector('[data-testid="external-class-filter"] [data-testid="external-outside-contract"]')).toBeNull();
    expect(chip.textContent).toBe('Outside contract');
    expect(chip.getAttribute('href')).toBe('#/fleet/externals?class=managed-cloud&outsideContract=1');
    expect(chip.getAttribute('aria-current')).toBeNull();
    done();
    const on = mountView({ klass: 'managed-cloud', outsideContract: '1' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenLastCalledWith(expect.objectContaining({ kinds: ['external'], class: 'managed-cloud', outsideContract: true })));
    const active = on.target.querySelector('[data-testid="external-outside-contract"]')!;
    expect(active.getAttribute('aria-current')).toBe('true');
    // The class navigation announces one current item, and the filter is listed and removable on its own.
    expect(on.target.querySelectorAll('[data-testid="external-class-filter"] [aria-current="true"]')).toHaveLength(1);
    const remove = on.target.querySelector('button[aria-label="Remove Contract filter"]') as HTMLButtonElement;
    remove.click();
    expect(location.hash).toBe('#/fleet/externals?class=managed-cloud');
    // Pressed again it clears the filter; a class chip keeps it.
    expect(active.getAttribute('href')).toBe('#/fleet/externals?class=managed-cloud');
    expect(on.target.querySelector('[data-class="vendor-api"]')?.getAttribute('href')).toBe('#/fleet/externals?class=vendor-api&outsideContract=1');
    on.done();
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
