/**
 * The service page as the one place a service's context is read: its contract, owner
 * (including when Pacto cannot tell), capabilities, the sources that saw it, the
 * environment of every target, every declared dependency (external and unresolved ones
 * too) and when each revision was fetched.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { fleetAttentionUrl, fleetServicesUrl } from '../../lib/router.ts';
import { formatDate } from '../../lib/dateFormat.ts';

const { detailFn } = vi.hoisted(() => ({ detailFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from '../FleetEntityView.svelte';

const KEY = 'domain-a/payments';
const preview = (items: unknown[], extra: Record<string, unknown> = {}) => ({
  total: items.length, count: items.length, truncated: false, items, ...extra,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const ref = (kind: string, key: string, label?: string, extra: any = {}): any => ({ kind, key, label: label ?? key, href: `/fleet/${kind}s/${encodeURIComponent(key)}`, ...extra });
const rev = (v: string, extra = {}) => ref('revision', `${KEY}@sha256:${v}`, `payments ${v}`, extra);

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function detail(service: Record<string, any> = {}, meta: Record<string, any> = {}): any {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 's', asOf: '2026-11-04T10:00:00Z', completeness: 'complete', sources: [{ id: 'oci', kind: 'oci', status: 'available' }], ...meta },
    entity: ref('service', KEY, 'payments'),
    actions: [],
    service: {
      summary: { targets: 0, revisions: 0, declaredDependencies: 0 },
      ownership: { declared: false },
      capabilities: preview([]),
      sources: preview([]),
      deployments: preview([]),
      activeRevisions: preview([]),
      revisions: preview([]),
      dependencies: preview([]),
      unresolvedDependencies: preview([]),
      dependents: preview([]),
      referencedBy: preview([]),
      limitations: preview([]),
      ...service,
    },
  };
}

async function render(d: unknown) {
  detailFn.mockResolvedValue(d);
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind: 'service', entityKey: KEY, refreshTick: 0 } });
  await vi.waitFor(() => expect(target.querySelector('.svc-entity')).toBeTruthy());
  flushSync();
  return { target, done: () => { unmount(component); target.remove(); } };
}

const fact = (t: HTMLElement, id: string) => t.querySelector(`[data-testid="${id}"]`) as HTMLElement | null;
const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();
const hrefs = (el: Element | null) => Array.from(el?.querySelectorAll('a[href]') ?? []).map((a) => a.getAttribute('href'));

describe('service page: contract fact', () => {
  beforeEach(() => detailFn.mockReset());

  it('links the latest revision in use', async () => {
    const { target, done } = await render(detail({ activeRevisions: preview([rev('2')]), revisions: preview([rev('3'), rev('2')]) }));
    const f = fact(target, 'service-contract');
    expect(hrefs(f)[0]).toContain(encodeURIComponent(`${KEY}@sha256:2`));
    expect(text(f)).toContain('Latest in use');
    done();
  });

  it('falls back to the latest known revision', async () => {
    const { target, done } = await render(detail({ revisions: preview([rev('3')]) }));
    const f = fact(target, 'service-contract');
    expect(hrefs(f)[0]).toContain(encodeURIComponent(`${KEY}@sha256:3`));
    expect(text(f)).toContain('Latest known');
    done();
  });

  it('says when there is no known revision', async () => {
    const { target, done } = await render(detail());
    expect(text(fact(target, 'service-contract'))).toContain('No known revision');
    done();
  });
});

describe('service page: owner fact', () => {
  beforeEach(() => detailFn.mockReset());
  const unowned = fleetAttentionUrl({ service: KEY, category: 'unowned' });

  it('links "No declared owner" to the unowned attention item', async () => {
    const { target, done } = await render(detail({ ownership: { declared: false } }));
    const f = fact(target, 'service-owner');
    expect(text(f)).toContain('No declared owner');
    expect(hrefs(f)).toContain(unowned);
    done();
  });

  it('says "Owner unknown" when Pacto cannot read the contracts', async () => {
    const { target, done } = await render(detail({ ownership: { declared: false, unknown: true } }));
    const f = fact(target, 'service-owner')!;
    expect(text(f)).toContain('Owner unknown');
    expect(text(f)).not.toContain('No declared owner');
    const a = f.querySelector('a[href]')!;
    expect(a.getAttribute('href')).toBe(unowned);
    expect(a.getAttribute('title')).toBe("Pacto could not read this service's contracts, so it cannot tell whether an owner is declared. Pacto still counts it as unowned.");
    done();
  });

  it('links the revision the owner is declared in', async () => {
    const owner = ref('owner', 'team/payments', 'team/payments');
    const { target, done } = await render(detail({ ownership: { declared: true, ref: owner }, ownerRevision: rev('1') }));
    const f = fact(target, 'service-owner')!;
    expect(hrefs(f)).toContain(`#${owner.href}`);
    expect(text(f)).toContain('Declared in');
    const declared = f.querySelector('[data-testid="owner-declared-in"]')!;
    expect(declared.getAttribute('title')).toBe("Its contacts are on that revision's page");
    expect(hrefs(declared)[0]).toContain(encodeURIComponent(`${KEY}@sha256:1`));
    done();
  });

  it('shows no "Declared in" without an owner revision', async () => {
    const { target, done } = await render(detail({ ownership: { declared: true, ref: ref('owner', 'team/payments') } }));
    expect(text(fact(target, 'service-owner'))).not.toContain('Declared in');
    done();
  });
});

describe('service page: capabilities and sources', () => {
  beforeEach(() => detailFn.mockReset());

  it('links each capability to the services that declare it', async () => {
    const { target, done } = await render(detail({ capabilities: preview(['health', 'metrics']) }));
    const h = hrefs(fact(target, 'service-capabilities'));
    expect(h).toEqual([fleetServicesUrl({ capability: 'health' }), fleetServicesUrl({ capability: 'metrics' })]);
    done();
  });

  it('says when no capability is declared', async () => {
    const { target, done } = await render(detail());
    expect(text(fact(target, 'service-capabilities'))).toContain('None declared');
    done();
  });

  it('says how many capabilities a truncated list leaves out', async () => {
    const { target, done } = await render(detail({ capabilities: preview(['health'], { total: 4, truncated: true }) }));
    expect(text(fact(target, 'service-capabilities'))).toContain('+3 more');
    done();
  });

  it('links each source that saw the service', async () => {
    const src = ref('source', 'k8s', 'k8s', { status: 'available' });
    const { target, done } = await render(detail({ sources: preview([src]) }));
    expect(hrefs(fact(target, 'service-sources'))).toContain(`#${src.href}`);
    done();
  });

  it('says when no source saw the service', async () => {
    const { target, done } = await render(detail());
    expect(text(fact(target, 'service-sources'))).toContain('No source');
    done();
  });
});

describe('service page: targets', () => {
  beforeEach(() => detailFn.mockReset());

  it('shows every target environment, and says when it is unknown', async () => {
    const long = 'e'.repeat(200);
    const { target, done } = await render(detail({
      summary: { targets: 3 },
      deployments: preview([
        ref('target', 'prod/k8s/payments', 'prod/k8s/payments', { environment: 'production' }),
        ref('target', 'dev/k8s/payments', 'dev/k8s/payments'),
        ref('target', 'qa/k8s/payments', 'qa/k8s/payments', { environment: long }),
      ]),
    }));
    const envs = Array.from(target.querySelectorAll('[data-testid="service-targets"] .pl-env'));
    expect(envs.map((e) => e.textContent)).toEqual(['production', 'Environment unknown', long]);
    expect(envs[1].classList.contains('is-unknown')).toBe(true);
    expect(envs[2].getAttribute('title')).toBe(long);
    done();
  });
});

describe('service page: expected dependencies', () => {
  beforeEach(() => detailFn.mockReset());

  it('counts every declared dependency and lists external and unresolved ones', async () => {
    const self = ref('service', KEY, 'payments');
    const { target, done } = await render(detail({
      summary: { declaredDependencies: 4 },
      dependencies: preview([ref('service', 'domain-b/ledger', 'ledger')]),
      unresolvedDependencies: preview([
        { from: self, ref: 'oci://reg/db', external: true, version: '15' },
        { from: self, ref: 'oci://reg/missing', reason: 'no provider' },
      ]),
      hiddenDependencies: 1,
    }));
    const sec = fact(target, 'service-dependencies')!;
    expect(text(sec)).toContain('4');
    const rows = Array.from(sec.querySelectorAll('[data-testid="unresolved-dependency"]'));
    expect(text(rows[0])).toContain('oci://reg/db');
    expect(text(rows[0])).toContain('external');
    expect(text(rows[0])).toContain('15');
    expect(text(rows[1])).toContain('unresolved');
    expect(rows[1].querySelector('[title="no provider"]')).toBeTruthy();
    expect(fact(target, 'hidden-dependencies')).toBeTruthy();
    done();
  });

  it('says when nothing is declared', async () => {
    const { target, done } = await render(detail());
    expect(text(fact(target, 'service-dependencies'))).toContain('No declared dependencies.');
    done();
  });
});

describe('service page: revision dates', () => {
  beforeEach(() => detailFn.mockReset());

  it('shows when each revision was fetched, and nothing when unknown', async () => {
    const at = '2026-10-01T10:00:00Z';
    const { target, done } = await render(detail({ revisions: preview([rev('2', { fetchedAt: at }), rev('1')]) }));
    const rows = Array.from(target.querySelectorAll('[data-testid="service-all-revisions"] li'));
    expect(text(rows[0])).toContain(`Fetched ${formatDate(at)}`);
    expect(text(rows[1])).not.toContain('Fetched');
    done();
  });
});

describe('service page: knowledge banner', () => {
  beforeEach(() => detailFn.mockReset());

  it("names only this service's degraded sources", async () => {
    const { target, done } = await render(detail(
      { sources: preview([ref('source', 'k8s', 'k8s')]) },
      { completeness: 'partial', sources: [{ id: 'k8s', status: 'unavailable' }, { id: 'edge', status: 'unavailable' }] },
    ));
    const banner = text(target.querySelector('.knowledge'));
    expect(banner).toContain('(k8s)');
    expect(banner).not.toContain('edge');
    done();
  });
});
