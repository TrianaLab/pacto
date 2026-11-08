/**
 * Component tests for the business-domain views: the domain list and page, the
 * overview breakdown, the Registry and Domain filters on Services, the capability
 * filter, the domain on a service page, the environment on targets and the domain
 * filter on attention. No domain and Unknown never read as healthy.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { entitiesFn, detailFn, overviewFn, attentionFn } = vi.hoisted(() => ({
  entitiesFn: vi.fn(), detailFn: vi.fn(), overviewFn: vi.fn(), attentionFn: vi.fn(),
}));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      fleetEntities: (...a: unknown[]) => entitiesFn(...a) ?? new Promise(() => {}),
      fleetEntityDetail: (...a: unknown[]) => detailFn(...a),
      fleetOverview: (...a: unknown[]) => overviewFn(...a),
      fleetAttention: (...a: unknown[]) => attentionFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetDomainsView from './FleetDomainsView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from './FleetEntityView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetOverview from './FleetOverview.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-11-04T10:00:00Z', completeness: 'complete', sources: [] };
const empty = { total: 0, count: 0, truncated: false, items: [] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function listResp(entities: any[], opts: { total?: number; aggregate?: any } = {}): any {
  return { meta, total: opts.total ?? entities.length, count: entities.length, offset: 0, limit: 25, truncated: false, entities, aggregate: opts.aggregate };
}
const domainRef = (name: string, services: number, targets: number) => (
  { kind: 'domain', key: name, label: name, businessDomain: name, services, targets, href: `/fleet/domains/${name}` });

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- Svelte components have no declaration files here
function mountView(Comp: any, props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(Comp, { target, props: { refreshTick: 0, ...props } });
  return { target, component, done: () => { unmount(component); document.body.removeChild(target); } };
}
const hrefs = (t: HTMLElement, sel: string) => Array.from(t.querySelectorAll(sel)).map((a) => a.getAttribute('href'));

beforeEach(() => {
  for (const f of [entitiesFn, detailFn, overviewFn, attentionFn]) f.mockReset();
  location.hash = '';
});

describe('FleetDomainsView', () => {
  it('lists named domains and ends with the No domain row', async () => {
    entitiesFn.mockImplementation((o: { kinds?: string[]; businessDomain?: string }) => Promise.resolve(o.kinds?.[0] === 'domain'
      ? listResp([domainRef('commerce', 12, 30), domainRef('identity', 1, 1)])
      : listResp([], { total: o.businessDomain === '(none)' ? 4 : 0 })));
    const { target, done } = mountView(FleetDomainsView);
    await vi.waitFor(() => expect(target.querySelector('[data-testid="domain-none"]')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['domain'], limit: 25 }));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], businessDomain: '(none)', limit: 1 }));
    const cells = (r: number) => Array.from(target.querySelectorAll('[data-testid="domain-list"] tbody tr')[r].querySelectorAll('td')).map((td) => td.textContent?.trim());
    expect(cells(0).slice(0, 2)).toEqual(['12', '30']);
    expect(cells(1).slice(0, 2)).toEqual(['1', '1']);
    expect(hrefs(target, '[data-testid="domain-list"] a.entity-link')).toEqual(['#/fleet/domains/commerce', '#/fleet/domains/identity']);
    const none = target.querySelector('[data-testid="domain-none"]') as HTMLElement;
    expect(none.textContent).toContain('No domain: 4 services');
    expect(none.querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?businessDomain=%28none%29');
    expect(none.className).not.toMatch(/ok/);
    done();
  });

  it('shows the services whose domain is unknown apart from No domain', async () => {
    entitiesFn.mockImplementation((o: { kinds?: string[]; businessDomain?: string }) => Promise.resolve(o.kinds?.[0] === 'domain'
      ? listResp([])
      : listResp([], { total: o.businessDomain === '(unknown)' ? 5 : 0 })));
    const { target, done } = mountView(FleetDomainsView);
    await vi.waitFor(() => expect(target.querySelector('[data-testid="domain-unknown"]')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], businessDomain: '(unknown)', limit: 1 }));
    const unknown = target.querySelector('[data-testid="domain-unknown"]') as HTMLElement;
    expect(unknown.textContent).toContain('Domain unknown: 5 services');
    expect(unknown.querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?businessDomain=%28unknown%29');
    // Five services may declare a domain Pacto cannot see: "none declared" would be a guess.
    expect(target.textContent).not.toContain('No service declares a domain yet.');
    expect(target.querySelector('[data-testid="domain-none"]')).toBeNull();
    done();
  });

  it('says a count failed instead of dropping the row', async () => {
    entitiesFn.mockImplementation((o: { kinds?: string[] }) => (o.kinds?.[0] === 'domain'
      ? Promise.resolve(listResp([domainRef('commerce', 1, 1)]))
      : Promise.reject(new Error('boom'))));
    const { target, done } = mountView(FleetDomainsView);
    await vi.waitFor(() => expect(target.querySelector('[data-testid="domain-count-error"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="domain-count-error"]')?.textContent).toContain('could not be counted');
    expect(target.textContent).not.toContain('No domain: 0');
    done();
  });

  it('explains how to declare a domain when none is declared', async () => {
    entitiesFn.mockImplementation((o: { kinds?: string[]; businessDomain?: string }) => Promise.resolve(o.kinds?.[0] === 'domain' ? listResp([])
      : listResp([], { total: o.businessDomain === '(none)' ? 3 : 0 })));
    const { target, done } = mountView(FleetDomainsView);
    await vi.waitFor(() => expect(target.textContent).toContain('No service declares a domain yet.'));
    expect(target.textContent).toContain('Set service.domain in a contract (pactoVersion 2.4) to group services by domain.');
    // The services that declare none are still reachable.
    await vi.waitFor(() => expect(target.querySelector('[data-testid="domain-none"]')?.textContent).toContain('No domain: 3 services'));
    done();
  });
});

describe('the domain page', () => {
  it('renders the posture, the previews and links scoped to the domain', async () => {
    detailFn.mockResolvedValue({
      meta, entity: { kind: 'domain', key: 'commerce', label: 'commerce', businessDomain: 'commerce', href: '/fleet/domains/commerce' },
      domain: {
        summary: { services: 2, revisions: 3, targets: 1, invalidRevisions: 0, compliance: { nonCompliant: 1 }, links: { exact: 1 }, evidence: {}, findings: {} },
        ownership: { consistent: 1, conflicting: 0, unowned: 1 },
        services: { total: 2, count: 1, truncated: true, items: [{ kind: 'service', key: 'payments', label: 'payments', href: '/fleet/services/payments' }] },
        owners: { total: 1, count: 1, truncated: false, items: [{ kind: 'owner', key: 'team:pay', label: 'pay', href: '/fleet/owners/team:pay' }] },
        deployments: { total: 1, count: 1, truncated: false, items: [{ kind: 'target', key: 'prod/payments', label: 'prod/payments', href: '/fleet/targets/prod%2Fpayments' }] },
        attention: { total: 3, count: 1, truncated: true, items: [{ severity: 'info', category: 'unowned', entity: { kind: 'service', key: 'ledger', label: 'ledger', href: '/fleet/services/ledger' }, summary: 'no owner declared' }] },
      },
    });
    entitiesFn.mockResolvedValue(listResp([], { aggregate: { services: 2, serviceCompliance: { nonCompliant: 1, compliant: 1 } } }));
    const { target, done } = mountView(FleetEntityView, { kind: 'domain', entityKey: 'commerce' });
    // The posture column counts the domain's services, and each row lists exactly them.
    await vi.waitFor(() => expect(target.querySelector('[data-testid="posture-figure"]')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith({ kinds: ['service'], limit: 1, businessDomain: 'commerce' });
    expect(target.querySelector('[data-testid="posture-figure"] .prow a')?.getAttribute('href')).toBe('#/fleet/services?status=NonCompliant&businessDomain=commerce');
    await vi.waitFor(() => expect(target.querySelector('.domain-entity')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).toContain('Operational summary');
    expect(text).toContain('Owners');
    expect(text).toContain('Operational targets');
    expect(text).toContain('No declared owner');
    const links = hrefs(target, 'a');
    expect(links).toContain('#/fleet/attention?businessDomain=commerce');
    expect(links).toContain('#/fleet/services?businessDomain=commerce');
    expect(links).toContain('#/fleet/services?ownership=unowned&businessDomain=commerce');
    expect(links).toContain('#/fleet/domains');
    expect(target.querySelector('[data-testid="entity-answer"]')).toBeTruthy();
    done();
  });
});

describe('FleetOverview — domain breakdown', () => {
  it('breaks services down by domain with a neutral No domain row', async () => {
    overviewFn.mockResolvedValue({
      meta: { ...meta, sourceCounts: { total: 1, available: 1 } },
      summary: {
        services: 5, revisions: 5, targets: 4, servicesNeedingAttention: 0,
        byScope: [{ scope: 'prod', targets: 4, nonCompliant: 1, unknown: 0, stale: 0 }, { scope: 'dev', targets: 0, nonCompliant: 0, unknown: 0, stale: 0 }], scopes: 2,
        byDomain: [
          { domain: 'commerce', services: 3, targets: 4, nonCompliant: 1, unknown: 0, stale: 0, unowned: 1 },
          { domain: '', domainUnknown: true, services: 1, targets: 0, nonCompliant: 0, unknown: 0, stale: 0, unowned: 0 },
          { domain: '', services: 2, targets: 0, nonCompliant: 0, unknown: 0, stale: 0, unowned: 2 },
        ],
        domains: 1,
      },
      attention: empty, recentEvidence: empty, entryPoints: [],
    });
    const { target, done } = mountView(FleetOverview);
    await vi.waitFor(() => expect(target.querySelector('[data-testid="overview-by-domain"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="overview-by-scope"] thead th')?.textContent).toBe('Scope');
    const table = target.querySelector('[data-testid="overview-by-domain"]') as HTMLElement;
    expect(Array.from(table.querySelectorAll('thead th')).map((th) => th.textContent))
      .toEqual(['Domain', 'Services', 'Targets', 'Not compliant', 'Unknown', 'Stale', 'No owner']);
    const rows = table.querySelectorAll('tbody tr');
    expect(rows[0].querySelector('a')?.getAttribute('href')).toBe('#/fleet/domains/commerce');
    const unknown = rows[1] as HTMLElement;
    expect(unknown.textContent).toContain('Domain unknown');
    expect(unknown.querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?businessDomain=%28unknown%29');
    const none = rows[2] as HTMLElement;
    expect(none.textContent).toContain('No domain');
    expect(none.querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?businessDomain=%28none%29');
    expect(none.innerHTML).not.toMatch(/tone-ok|ov-ok/);
    expect(hrefs(target, 'a')).toContain('#/fleet/domains');
    done();
  });
});
