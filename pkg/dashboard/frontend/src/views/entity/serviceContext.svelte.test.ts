/**
 * The service page as the one place a service's context is read: its contract, owner
 * (including when Pacto cannot tell), capabilities, the sources that saw it, the
 * environment of every target, every declared dependency (external and unresolved ones
 * too) and when each revision was fetched.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { fleetAttentionUrl, fleetServicesUrl, fleetGraphFocusUrl, fleetEntityListUrl } from '../../lib/router.ts';
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

describe('service page: dependency drift', () => {
  beforeEach(() => detailFn.mockReset());

  it('links each drift row to where its dependencies are listed', async () => {
    const { target, done } = await render(detail({
      summary: { declaredDependencies: 6, reconciledMatched: 1, declaredNotObserved: 1, observedNotDeclared: 1, unresolvedDeclared: 1, unsatisfiedRanges: 2, insufficientEvidence: 1 },
    }));
    const graph = fleetGraphFocusUrl('service', KEY);
    const rows = Array.from(target.querySelectorAll('.se-drift li'));
    const row = (label: string) => rows.find((r) => text(r).endsWith(label))!;
    expect(text(row('Outside accepted range'))).toBe('2 Outside accepted range');
    expect(hrefs(row('Outside accepted range'))).toEqual([fleetAttentionUrl({ service: KEY, category: 'out-of-range' })]);
    expect(text(row('Cannot be observed'))).toBe('1 Cannot be observed');
    expect(hrefs(row('Cannot be observed'))[0]).toBe(graph);
    expect(hrefs(row('Unresolved declarations'))).toEqual([fleetAttentionUrl({ service: KEY, category: 'unresolved' })]);
    expect(hrefs(row('Declared, not observed'))[0]).toBe(graph);
    expect(hrefs(row('Observed, not declared'))[0]).toBe(graph);
    expect(hrefs(row('Declared and observed'))).toEqual([]);
    expect(hrefs(row('Declared dependencies'))).toEqual([]);
    done();
  });

  it('hides zero rows', async () => {
    const { target, done } = await render(detail({ summary: { declaredDependencies: 1, reconciledMatched: 1 } }));
    expect(text(target.querySelector('.se-drift'))).not.toContain('Outside accepted range');
    expect(text(target.querySelector('.se-drift'))).not.toContain('Cannot be observed');
    done();
  });
});

describe('service page: relationships', () => {
  beforeEach(() => detailFn.mockReset());
  const self = ref('service', KEY, 'payments');
  const edge = (rangeStatus: string, extra = {}) => ({
    id: `e-${rangeStatus}`, relation: 'dependency', expected: true, observed: true, provenance: 'declared+observed', difference: 'matched',
    from: self, to: ref('service', `domain-b/${rangeStatus}`, rangeStatus),
    declaredClaims: preview([{ required: true, rangeStatus }]), observationSources: preview([]), ...extra,
  });

  it('is always rendered and says when no traffic was observed', async () => {
    const { target, done } = await render(detail());
    expect(text(fact(target, 'service-relationships'))).toContain('No traffic observed for this service yet.');
    done();
  });

  it('says when relationships could not be computed', async () => {
    const { target, done } = await render(detail({ limitations: preview([{ limitation: { code: 'NEIGHBORHOOD_UNAVAILABLE', message: 'm' } }]) }));
    expect(text(fact(target, 'service-relationships'))).toContain('Relationships could not be computed. See the limitations below.');
    done();
  });

  it('shows the range of each declared claim and when traffic was last seen', async () => {
    const at = '2026-11-01T10:00:00Z';
    const { target, done } = await render(detail({
      relationships: preview([edge('unsatisfied', { count: 42, lastSeen: at }), edge('unknown'), edge('satisfied')]),
    }));
    const sec = fact(target, 'service-relationships')!;
    const badge = (label: string) => Array.from(sec.querySelectorAll('.tag')).find((b) => text(b) === label);
    expect(text(sec)).toContain('Outside range');
    expect(text(sec)).toContain('Range unknown');
    expect(text(sec)).toContain('In range');
    expect(badge('Outside range')?.className).toContain('tone-warn');
    expect(badge('Range unknown')?.className).toContain('tone-neutral');
    expect(badge('In range')?.className).toContain('tone-ok');
    expect(text(sec)).toContain(`Last seen ${formatDate(at)} · 42 calls`);
    done();
  });
});

describe('service page: readiness and signatures', () => {
  beforeEach(() => detailFn.mockReset());

  it('draws the readiness of known revisions, each bucket linking its revisions', async () => {
    const { target, done } = await render(detail({ summary: { revisions: 3, readiness: { passing: 1, expired: 2 } } }));
    const fig = Array.from(target.querySelectorAll('figure.dist')).find((f) => text(f).includes('Readiness of known revisions'))!;
    expect(fig).toBeTruthy();
    expect(hrefs(fig)).toContain(fleetEntityListUrl('revision', { service: KEY, readiness: 'passing' }));
    expect(hrefs(fig)).toContain(fleetEntityListUrl('revision', { service: KEY, readiness: 'expired' }));
    done();
  });

  it('draws the bundle signatures of revisions in use', async () => {
    const { target, done } = await render(detail({ summary: { revisionsInUse: 3, signatures: { verified: 1, notChecked: 2 } } }));
    const sec = target.querySelector('.se-summary')!;
    expect(text(sec)).toContain('Bundle signatures of revisions in use');
    expect(text(sec)).toContain('Not checked');
    done();
  });

  it('states it in one neutral line when no source checks signatures', async () => {
    const { target, done } = await render(detail({ summary: { revisionsInUse: 3, signatures: { notChecked: 3 } } }));
    const sec = target.querySelector('.se-summary')!;
    expect(text(sec)).toContain('No source that read these revisions checks signatures.');
    expect(text(sec)).not.toContain('Bundle signatures of revisions in use');
    done();
  });

  it('lists the signature of each revision in use', async () => {
    const { target, done } = await render(detail({
      signatures: preview([
        { revision: rev('3'), checked: true, referrers: 2, signature: { status: 'verified', signer: 'ci@acme.dev', bundles: 1 } },
        { revision: rev('2'), checked: true, referrers: 1, signature: { status: 'unverified', signer: 'evil@x.dev', bundles: 1 } },
        { revision: rev('1'), checked: false, referrers: 0 },
        { revision: rev('0'), checked: true, referrers: 0, signature: { status: 'verified', bundles: 1 } },
      ]),
    }));
    const rows = Array.from(fact(target, 'service-signatures')!.querySelectorAll('li'));
    expect(hrefs(rows[0])[0]).toContain(encodeURIComponent(`${KEY}@sha256:3`));
    expect(text(rows[0])).toContain('Verified');
    expect(text(rows[0])).toContain('Signer ci@acme.dev, declared by its own contract');
    expect(text(rows[0])).toContain('2 referrers');
    expect(text(rows[1])).toContain('Unverified');
    expect(text(rows[1])).toContain('Signer evil@x.dev, declared by its own contract');
    expect(text(rows[2])).toContain('Not checked');
    expect(text(rows[3])).not.toContain('Verified');
    done();
  });
});

describe('service page: policies and configuration', () => {
  beforeEach(() => detailFn.mockReset());
  const content = { properties: [{ key: 'tls', type: 'boolean', value: 'true' }], total: 1, truncated: false };

  it("shows the current revision's policies with their content", async () => {
    const { target, done } = await render(detail({
      currentRevision: rev('3'),
      policies: preview([
        { name: 'pii', schema: 'policy/schema.json', content },
        { name: 'http-security', ref: 'oci://acme/policies:1', resolution: { resolved: true, service: ref('service', 'domain-a/policies', 'policies') } },
      ]),
    }));
    const sec = fact(target, 'service-policies')!;
    expect(text(sec)).toContain('pii');
    expect(text(sec)).toContain('tls');
    expect(text(sec)).toContain('From');
    expect(hrefs(sec).some((h) => h?.includes(encodeURIComponent(`${KEY}@sha256:3`)))).toBe(true);
    expect(text(sec)).toContain('Defined by the referenced contract');
    expect(text(sec)).toContain('oci://acme/policies:1');
    done();
  });

  it('says when the current revision declares no policies or configuration', async () => {
    const { target, done } = await render(detail({ currentRevision: rev('3'), policies: preview([]), configurations: preview([]) }));
    expect(text(fact(target, 'service-policies'))).toContain("This service's current revision declares no policies.");
    expect(text(fact(target, 'service-configuration'))).toContain("This service's current revision declares no configuration.");
    done();
  });

  it("shows the current revision's configuration with its content", async () => {
    const { target, done } = await render(detail({
      currentRevision: rev('3'),
      configurations: preview([{ name: 'app', required: true, schema: 'configuration/schema.json', values: preview([]), content }]),
    }));
    const sec = fact(target, 'service-configuration')!;
    expect(text(sec)).toContain('app');
    expect(text(sec)).toContain('tls');
    expect(text(sec)).toContain('From');
    done();
  });
});
