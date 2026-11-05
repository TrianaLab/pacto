/**
 * Component tests for the Registry, Domain and capability filters on Services, the
 * domain on a service page, the environment on targets and the domain filter on
 * attention. No domain and Unknown never read as healthy.
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
      fleetEntities: (...a: unknown[]) => entitiesFn(...a),
      fleetEntityDetail: (...a: unknown[]) => detailFn(...a),
      fleetOverview: (...a: unknown[]) => overviewFn(...a),
      fleetAttention: (...a: unknown[]) => attentionFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from './FleetEntityView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetServicesView from './FleetServicesView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetEntityListView from './FleetEntityListView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import FleetAttentionView from './FleetAttentionView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-11-04T10:00:00Z', completeness: 'complete', sources: [] };
const empty = { total: 0, count: 0, truncated: false, items: [] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function listResp(entities: any[], opts: { total?: number; aggregate?: any } = {}): any {
  return { meta, total: opts.total ?? entities.length, count: entities.length, offset: 0, limit: 25, truncated: false, entities, aggregate: opts.aggregate };
}

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

describe('FleetServicesView — Registry, Domain and capability filters', () => {
  it('calls the registry path Registry and filters by domain and capability', async () => {
    entitiesFn.mockResolvedValue(listResp(
      [{ kind: 'service', key: 'payments', label: 'payments', href: '/fleet/services/payments', businessDomain: 'commerce' }],
      { aggregate: { matched: 1, services: 1, capabilities: [{ type: 'health', services: 1 }, { type: 'metrics', services: 3 }], capabilityTypes: 2 } },
    ));
    const { target, done } = mountView(FleetServicesView, { businessDomain: '(none)', capability: 'health', domain: 'ghcr.io/acme' });
    await vi.waitFor(() => expect(target.querySelectorAll('.sv-item').length).toBe(1));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ businessDomain: '(none)', capability: 'health', domain: 'ghcr.io/acme' }));
    const text = target.textContent || '';
    expect(text).toContain('Registry');
    expect(target.querySelector('input[aria-label="Filter by registry path"]')?.getAttribute('placeholder')).toBe('exact registry path');
    expect(target.querySelector('[data-testid="svc-search"]')?.getAttribute('aria-label')).toBe('Search services by name, key or registry');
    expect(target.querySelector('[data-testid="svc-domain"]')).toBeTruthy();
    const opts = Array.from(target.querySelectorAll('select[aria-label="Filter by self-declared capability"] option')).map((o) => o.textContent);
    expect(opts).toEqual(['Any', 'health (1)', 'metrics (3)']);
    expect(text).toContain('Declared capability');
    const chips = Array.from(target.querySelectorAll('.chip, [data-testid="filter-chip"]')).map((c) => c.textContent?.replace(/\s+/g, ' ').trim());
    expect(chips.join(' | ')).toMatch(/Registry/);
    expect(chips.join(' | ')).toMatch(/Domain.*No domain/);
    expect(chips.join(' | ')).toMatch(/Declared capability.*health/);
    done();
  });

  it('filters by platform offering from the aggregate', async () => {
    entitiesFn.mockResolvedValue(listResp(
      [{ kind: 'service', key: 'redis', label: 'redis', href: '/fleet/services/redis', offering: 'redis' }],
      { aggregate: { matched: 1, services: 1, offerings: [{ type: 'shared-config', services: 2 }, { type: 'redis', services: 1 }], offeringCount: 0 } },
    ));
    const { target, done } = mountView(FleetServicesView, { offering: 'postgresql' });
    await vi.waitFor(() => expect(target.querySelectorAll('.sv-item').length).toBe(1));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], offering: 'postgresql' }));
    const sel = target.querySelector('select[aria-label="Filter by platform offering"]') as HTMLSelectElement;
    // A selected offering the match no longer holds stays listed, so the select never reads Any.
    expect(Array.from(sel.options).map((o) => o.textContent)).toEqual(['Any', 'postgresql (0)', 'shared-config (2)', 'redis (1)']);
    const chips = Array.from(target.querySelectorAll('.chip, [data-testid="filter-chip"]')).map((c) => c.textContent?.replace(/\s+/g, ' ').trim());
    expect(chips.join(' | ')).toMatch(/Offering.*postgresql/);
    sel.value = 'redis';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(location.hash).toBe('#/fleet/services?offering=redis');
    done();
  });
});

describe('the service page links its domain', () => {
  const svc = (businessDomain?: string, businessDomainUnknown?: boolean) => ({
    meta, status: 'Compliant',
    entity: { kind: 'service', key: 'payments', label: 'payments', href: '/fleet/services/payments', businessDomain, businessDomainUnknown },
    service: { revisions: empty, deployments: empty, dependencies: empty, dependents: empty, relationships: empty, findings: empty, evidence: empty, limitations: empty, summary: {} },
  });

  it('links a declared domain', async () => {
    detailFn.mockResolvedValue(svc('commerce'));
    const { target, done } = mountView(FleetEntityView, { kind: 'service', entityKey: 'payments' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="service-domain"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="service-domain"] a')?.getAttribute('href')).toBe('#/fleet/domains/commerce');
    done();
  });

  it('says No domain in a neutral tone without one', async () => {
    detailFn.mockResolvedValue(svc());
    const { target, done } = mountView(FleetEntityView, { kind: 'service', entityKey: 'payments' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="service-domain"]')).toBeTruthy());
    const fact = target.querySelector('[data-testid="service-domain"]') as HTMLElement;
    expect(fact.textContent).toContain('No domain');
    expect(fact.querySelector('a')).toBeNull();
    expect(fact.querySelector('.tone-neutral')).toBeTruthy();
    done();
  });

  it('says the domain is unknown, never No domain, when Pacto cannot tell', async () => {
    detailFn.mockResolvedValue(svc(undefined, true));
    const { target, done } = mountView(FleetEntityView, { kind: 'service', entityKey: 'payments' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="service-domain"]')).toBeTruthy());
    const fact = target.querySelector('[data-testid="service-domain"]') as HTMLElement;
    expect(fact.textContent).toContain('Domain unknown');
    expect(fact.textContent).not.toContain('No domain');
    expect(fact.innerHTML).not.toMatch(/declares service\.domain/);
    done();
  });
});

describe('the target page shows its environment', () => {
  const tgt = (environment: Record<string, unknown>) => ({
    meta, status: 'Compliant',
    entity: { kind: 'target', key: 'prod/payments', label: 'prod/payments', href: '/fleet/targets/prod%2Fpayments' },
    target: { linkState: 'exact', identity: { retrievable: true }, service: { kind: 'service', key: 'payments', label: 'payments', href: '/fleet/services/payments' }, environment },
  });
  const env = (t: HTMLElement) => t.querySelector('[data-testid="target-environment"]') as HTMLElement;

  it('reads Unknown, neutral, with its reason', async () => {
    detailFn.mockResolvedValue(tgt({ declared: 'unknown', reason: 'The Pacto carries no environment label.' }));
    const { target, done } = mountView(FleetEntityView, { kind: 'target', entityKey: 'prod/payments' });
    await vi.waitFor(() => expect(env(target)).toBeTruthy());
    expect(env(target).querySelector('.tone-neutral')?.textContent).toBe('Unknown');
    expect(env(target).textContent).toContain('The Pacto carries no environment label.');
    expect(env(target).textContent).not.toContain('Values check');
    done();
  });

  it('reads a declared environment whose values pass in the ok tone', async () => {
    detailFn.mockResolvedValue(tgt({ name: 'prod', declared: 'declared', check: { name: 'prod', file: 'values/prod.yaml', status: 'pass', findings: empty } }));
    const { target, done } = mountView(FleetEntityView, { kind: 'target', entityKey: 'prod/payments' });
    await vi.waitFor(() => expect(env(target)).toBeTruthy());
    expect(env(target).querySelector('.tone-ok')?.textContent).toBe('prod');
    expect(env(target).textContent).toContain('Values check: pass');
    done();
  });

  it('warns when the running revision does not declare the environment', async () => {
    detailFn.mockResolvedValue(tgt({ name: 'staging', declared: 'not-declared', reason: 'The running revision declares no staging environment.' }));
    const { target, done } = mountView(FleetEntityView, { kind: 'target', entityKey: 'prod/payments' });
    await vi.waitFor(() => expect(env(target)).toBeTruthy());
    expect(env(target).querySelector('.tone-warn')?.textContent).toBe('staging');
    done();
  });
});

describe('FleetEntityListView — target environment', () => {
  it('filters by environment and says Environment unknown on a row without one', async () => {
    entitiesFn.mockResolvedValue(listResp([
      { kind: 'target', key: 'a', label: 'a', href: '/fleet/targets/a', environment: 'prod' },
      { kind: 'target', key: 'b', label: 'b', href: '/fleet/targets/b' },
    ]));
    const { target, done } = mountView(FleetEntityListView, { kind: 'target', environment: 'prod' });
    await vi.waitFor(() => expect(target.querySelectorAll('.lv-item').length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['target'], environment: 'prod' }));
    const rows = target.querySelectorAll('.lv-item');
    expect(rows[0].textContent).toContain('prod');
    expect(rows[1].textContent).toContain('Environment unknown');
    expect(target.textContent).toContain('Environment');
    expect(target.querySelector('input[aria-label="Filter by environment"]')).toBeTruthy();
    done();
  });

  it('sends no environment for revisions', async () => {
    entitiesFn.mockResolvedValue(listResp([]));
    const { done } = mountView(FleetEntityListView, { kind: 'revision', environment: 'prod' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    expect(entitiesFn.mock.calls[0][0].environment).toBeUndefined();
    done();
  });
});

describe('FleetAttentionView — domain filter and the unowned category', () => {
  it('passes the domain to the api, shows a chip and offers the unowned category', async () => {
    attentionFn.mockResolvedValue({ meta, total: 0, count: 0, offset: 0, limit: 25, truncated: false, items: [] });
    const { target, done } = mountView(FleetAttentionView, { businessDomain: 'commerce' });
    await vi.waitFor(() => expect(attentionFn).toHaveBeenCalled());
    expect(attentionFn).toHaveBeenCalledWith(expect.objectContaining({ businessDomain: 'commerce' }));
    const cats = Array.from(target.querySelectorAll('select[aria-label="Filter by category"] option')).map((o) => o.textContent);
    expect(cats).toContain('No declared owner');
    await vi.waitFor(() => expect(target.textContent).toMatch(/Domain\s*:?\s*commerce/));
    done();
  });
});
