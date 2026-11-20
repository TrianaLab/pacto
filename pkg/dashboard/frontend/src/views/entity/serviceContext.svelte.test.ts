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

const { detailFn, entitiesFn } = vi.hoisted(() => ({ detailFn: vi.fn(), entitiesFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a), fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
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

async function render(d: unknown, props: Record<string, unknown> = {}) {
  detailFn.mockResolvedValue(d);
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind: 'service', entityKey: KEY, refreshTick: 0, ...props } });
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
    expect(a.getAttribute('title')).toBe("Pacto could not read this service's contracts, so it cannot tell whether an owner is declared. Pacto counts it as owner unknown, never as unowned.");
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

  it('labels the capabilities as self-declared and explains them', async () => {
    const { target, done } = await render(detail());
    const f = fact(target, 'service-capabilities');
    expect(text(f)).toContain('Declared capabilities');
    expect(f?.querySelector('button')?.getAttribute('aria-label') ?? '').toMatch(/Declared capabilities/);
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

  // A source that went down reports nothing, so it is never among the sources
  // that saw this service: filtering by them would drop the one that failed.
  it('names every degraded source behind its count', async () => {
    const { target, done } = await render(detail(
      { sources: preview([ref('source', 'k8s', 'k8s')]) },
      { completeness: 'partial', sources: [{ id: 'k8s', status: 'unavailable' }, { id: 'edge', status: 'unavailable' }] },
    ));
    const banner = text(target.querySelector('.knowledge'));
    expect(banner).toContain('k8s');
    expect(banner).toContain('edge');
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

  it('names a claim with compatibility unknown Range not declared, never in range', async () => {
    const { target, done } = await render(detail({
      relationships: preview([edge('satisfied', { declaredClaims: preview([{ required: true, compatibility: 'unknown', rangeStatus: 'satisfied' }]) })]),
    }));
    const sec = fact(target, 'service-relationships')!;
    expect(text(sec)).toContain('Range not declared');
    expect(text(sec)).not.toContain('In range');
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
        { revision: rev('2'), checked: true, referrers: 1, signature: { status: 'unverified', expectedSigner: 'ci@acme.dev', bundles: 1, reason: 'does not verify against ci@acme.dev' } },
        { revision: rev('1'), checked: false, referrers: 0 },
        { revision: rev('0'), checked: true, referrers: 0, signature: { status: 'verified', bundles: 1 } },
        { revision: rev('4'), checked: true, referrers: 0, referrersUnavailable: 'registry down', signature: { status: 'unknown', bundles: 0, reason: 'registry down' } },
      ]),
    }));
    const rows = Array.from(fact(target, 'service-signatures')!.querySelectorAll('li'));
    expect(hrefs(rows[0])[0]).toContain(encodeURIComponent(`${KEY}@sha256:3`));
    expect(text(rows[0])).toContain('Verified');
    expect(text(rows[0])).toContain('Signer ci@acme.dev, declared by its own contract');
    expect(text(rows[0])).toContain('2 referrers');
    expect(text(rows[1])).toContain('Unverified');
    // The declared identity of an unverified signature is only what was expected,
    // and the reason is readable without hovering.
    expect(text(rows[1])).toContain('Expected signer ci@acme.dev, declared by its own contract');
    expect(text(rows[1])).not.toMatch(/(^|[^d] )Signer /);
    expect(rows[1].querySelector('.visually-hidden')?.textContent).toBe('does not verify against ci@acme.dev');
    expect(text(rows[2])).toContain('Not checked');
    expect(text(rows[3])).not.toContain('Verified');
    // Referrers Pacto could not list are not zero referrers.
    expect(text(rows[4])).toContain('Referrers unavailable');
    expect(text(rows[4])).not.toContain('0 referrers');
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

  it('says which provider revision and configuration a ref configuration resolves to', async () => {
    const pgRev = ref('revision', 'ghcr.io/acme/postgresql@sha256:16', 'postgresql 16.1.0', { version: '16.1.0' });
    const pgSvc = ref('service', 'ghcr.io/acme/postgresql', 'postgresql');
    const { target, done } = await render(detail({
      currentRevision: rev('3'),
      configurations: preview([
        { name: 'db', required: true, ref: 'oci://ghcr.io/acme/postgresql@sha256:16', configuration: 'database', values: preview([]), content, resolution: { resolved: true, service: pgSvc, revision: pgRev } },
        { name: 'cache', required: false, ref: 'oci://ghcr.io/acme/cache@sha256:1', values: preview([]), resolution: { resolved: true, service: pgSvc, revision: ref('revision', 'k', 'cache sha256:1') } },
        { name: 'unresolved', required: false, ref: 'oci://ghcr.io/acme/x:1', configuration: 'main', values: preview([]), resolution: { resolved: false } },
      ]),
    }));
    const notes = Array.from(fact(target, 'service-configuration')!.querySelectorAll('.cl-note')).map(text);
    expect(notes).toEqual(['From postgresql@16.1.0, configuration database', 'From cache sha256:1', 'Defined by the referenced contract, configuration main']);
    done();
  });
});

describe('service page: platform offerings', () => {
  beforeEach(() => detailFn.mockReset());
  const pg = ref('service', 'ghcr.io/acme/postgresql', 'postgresql');
  const cfg = ref('service', 'ghcr.io/acme/platform-app-config', 'platform-app-config');

  const res = (t: HTMLElement) => t.querySelector('[data-testid="service-resources"]') as HTMLElement | null;
  const section = (t: HTMLElement) => target(t, 'Resources');
  const target = (t: HTMLElement, title: string) => Array.from(t.querySelectorAll('[data-testid="preview-section"]'))
    .find((s) => s.querySelector('h2')?.textContent === title) as HTMLElement;
  const FOOTER = "Resize or delete runs in the provider's tools. pacto_edit refuses a request the provider's schema rejects, and pacto validate and push fail on it.";

  it('lists each resource the service uses, with its provider, offering and range', async () => {
    const used = preview([
      { offering: 'postgresql', consumer: ref('service', KEY, 'payments'), provider: pg, via: 'dependency', compatibility: '^16.0.0', rangeStatus: 'satisfied' },
      { offering: 'shared-config', consumer: ref('service', KEY, 'payments'), provider: cfg, via: 'configuration_reference' },
    ], { phases: { failed: 0 } });
    const { target: t, done } = await render(detail({ consumedOfferings: used, inferredOfferings: [] }));
    const f = res(t);
    expect(hrefs(f)).toEqual(expect.arrayContaining(['#/fleet/offerings/postgresql', '#/fleet/offerings/shared-config']));
    expect(f?.querySelector('.tone-ok')?.textContent).toBe('satisfied');
    expect(f?.querySelector('[data-testid="provisioning-badge"]')).toBeNull();
    expect(text(fact(t, 'service-resources-fact'))).toBe('Resources 2');
    done();
  });

  it('shows what providers report and the provider lifecycle on each resource', async () => {
    const used = preview([
      { offering: 'postgresql', consumer: ref('service', KEY, 'payments'), provider: pg, via: 'configuration_reference',
        providerLifecycle: { deprecated: '2026-01-01', sunset: '2999-01-01' },
        provisioning: [
          { environment: 'production', phase: 'Failed', provenance: 'observed', verified: true, producer: 'adapter', source: 'evidence', asOf: '2026-11-04T09:00:00Z' },
          { environment: 'staging', phase: 'Unknown', provenance: 'observed', verified: false, stale: true, reportedPhase: 'Ready' },
        ] },
    ], { phases: { failed: 1 } });
    const { target: t, done } = await render(detail({ consumedOfferings: used, inferredOfferings: [] }));
    const f = res(t)!;
    const tags = Array.from(f.querySelectorAll('[data-testid="provisioning-badge"] .tag'));
    expect(tags.map((x) => x.textContent)).toEqual(['production: Failed', 'staging: Unknown']);
    expect(tags[0].classList.contains('tone-err')).toBe(true);
    expect(text(f)).toContain('Unknown: last reported Ready, stale');
    expect(text(f)).toContain('Deprecated');
    expect(text(fact(t, 'service-resources-fact'))).toBe('Resources 1, 1 failed request');
    done();
  });

  it('never says None when the service uses no resource, and keeps the footer', async () => {
    const { target: t, done } = await render(detail());
    const s = section(t);
    expect(text(s)).toContain('No resource you can see. References that are not digest-pinned or locked are not counted.');
    expect(text(s)).not.toContain('None');
    expect(t.querySelector('[data-testid="hidden-providers"]')).toBeNull();
    expect(text(fact(t, 'resources-footer'))).toContain(FOOTER);
    expect(fact(t, 'resources-footer')?.querySelector('a')?.getAttribute('href')).toBe('https://pacto.run/latest/provision-infrastructure/');
    done();
  });

  it('leads with the hidden count when every resource is from a provider the viewer may not see', async () => {
    const { target: t, done } = await render(detail({ consumedOfferings: preview([]), hiddenProviders: 2 }));
    const s = section(t);
    expect(text(s)).not.toContain('None');
    expect(text(s.querySelector('[data-testid="hidden-providers"]'))).toBe('At least 2 resources from providers you may not see.');
    expect(t.querySelectorAll('[data-testid="hidden-providers"]').length).toBe(1);
    // The header fact never reads a confident zero while providers are hidden.
    expect(text(fact(t, 'service-resources-fact'))).toBe('Resources 0, at least 2 you may not see');
    expect(text(fact(t, 'resources-footer'))).toContain(FOOTER);
    done();
  });

  it('says Showing N of M once when the list is cut short', async () => {
    const items = Array.from({ length: 3 }, (_, i) => ({ offering: '', consumer: ref('service', KEY, 'payments'), provider: ref('service', `p${i}`), via: 'dependency' }));
    const { target: t, done } = await render(detail({ consumedOfferings: preview(items, { total: 40, truncated: true }) }));
    expect(section(t).querySelectorAll('[data-testid="preview-more"]').length).toBe(1);
    expect(text(section(t).querySelector('[data-testid="preview-more"]'))).toBe('Showing 3 of 40.');
    done();
  });

  it('jumps to the section without touching the route', async () => {
    const { target: t, done } = await render(detail());
    const before = location.hash;
    const el = t.querySelector('#sec-resources') as HTMLElement;
    const spy = vi.fn();
    el.scrollIntoView = spy;
    (fact(t, 'service-resources-fact')!.querySelector('button') as HTMLButtonElement).click();
    expect(spy).toHaveBeenCalled();
    expect(location.hash).toBe(before);
    done();
  });

  it('lists a configuration entry a resource requests by name only, with a jump to Resources', async () => {
    const used = preview([{ offering: 'postgresql', consumer: ref('service', KEY, 'payments'), provider: pg, via: 'configuration_reference',
      requested: [{ entry: 'database', provenance: 'declared', consumerRevision: rev('1'), values: preview([{ key: 'size', value: 'medium' }], { scanned: 1 }) }] }]);
    const configurations = preview([
      { name: 'database', ref: 'oci://ghcr.io/acme/postgresql:16.1.0', resolution: { service: pg }, values: preview([{ key: 'size', value: 'medium' }]) },
      { name: 'main', schema: 'configuration/schema.json' },
    ]);
    const { target: t, done } = await render(detail({ consumedOfferings: used, configurations, currentRevision: rev('1') }));
    const cfgs = fact(t, 'service-configuration')!;
    expect(text(cfgs.querySelector('[data-testid="configuration-resource"]'))).toBe('database Requested from postgresql, see Resources');
    expect(cfgs.querySelectorAll('[data-testid="configuration-resource"]').length).toBe(1);
    expect(cfgs.querySelectorAll('.kv-table').length).toBe(0);
    expect(text(section(t).querySelector('[data-testid="preview-count"]'))).toBe('1 of 1');
    done();
  });

  it('keeps the current entry whole when another revision makes the request', async () => {
    const used = preview([{ offering: 'postgresql', consumer: ref('service', KEY, 'payments'), provider: pg, via: 'configuration_reference',
      requested: [{ entry: 'database', provenance: 'declared', consumerRevision: rev('2'), values: preview([{ key: 'size', value: 'large' }], { scanned: 1 }) }] }]);
    const configurations = preview([
      { name: 'database', ref: 'oci://ghcr.io/acme/postgresql:16.1.0', resolution: { service: pg }, values: preview([{ key: 'size', value: 'small' }]) },
    ]);
    const { target: t, done } = await render(detail({ consumedOfferings: used, configurations, currentRevision: rev('1') }));
    const cfgs = fact(t, 'service-configuration')!;
    expect(cfgs.querySelector('[data-testid="configuration-resource"]')).toBeNull();
    expect(text(cfgs)).toContain('small');
    expect(text(res(t))).toContain('declared by payments 2');
    done();
  });

  it('marks an inferred provider on its dependency row and never counts it as used', async () => {
    const unresolved = preview([{ ref: 'oci://other.io/x/redis', requestedRef: 'oci://other.io/x/redis', reason: 'no match' }]);
    const inferred = [{ ref: 'oci://other.io/x/redis', offering: ref('offering', 'redis', 'redis', { href: '/fleet/offerings/redis' }), reason: 'offering-name' }];
    const { target, done } = await render(detail({ unresolvedDependencies: unresolved, inferredOfferings: inferred, consumedOfferings: preview([]) }));
    const row = target.querySelector('[data-testid="unresolved-dependency"]') as HTMLElement;
    expect(text(row)).toContain('inferred');
    expect(text(row)).toContain('Matched by name only; the contract does not declare it');
    const link = row.querySelector('[data-testid="inferred-offering"]') as HTMLAnchorElement;
    expect(link.textContent).toBe('may be the redis offering');
    expect(link.getAttribute('href')).toBe('#/fleet/offerings/redis');
    expect(text(target.querySelector('#sec-resources'))).toContain('No resource you can see.');
    expect(hrefs(target.querySelector('#sec-resources'))).not.toContain('#/fleet/offerings/redis');
    done();
  });

  it('shows the offering the service provides and its summary under the title', async () => {
    const d = detail();
    d.entity = { ...d.entity, offering: 'postgresql', summary: 'The primary database for payments.' };
    const { target, done } = await render(d);
    expect(hrefs(fact(target, 'service-offering'))).toEqual(['#/fleet/offerings/postgresql']);
    expect(target.querySelector('.page-hd-sub')?.textContent).toBe('The primary database for payments.');
    done();
  });

  it('shows no Provides fact or summary when the service declares neither', async () => {
    const { target, done } = await render(detail());
    expect(target.querySelector('[data-testid="service-offering"]')).toBeNull();
    expect(target.querySelector('.page-hd-sub')).toBeNull();
    done();
  });
});

describe('service page: open in Mira', () => {
  beforeEach(() => { detailFn.mockReset(); entitiesFn.mockReset(); });
  const svc = (domain: string) => ref('service', `${domain}/payments`, 'payments');
  const mira = (t: HTMLElement) => t.querySelector('[data-testid="service-relationships"] [data-testid="service-mira-link"]');

  it('links the service in Mira when its name is unique', async () => {
    entitiesFn.mockResolvedValue({ entities: [svc('domain-a')], truncated: false });
    const { target, done } = await render(detail(), { miraURL: 'https://mira.example' });
    expect(entitiesFn).toHaveBeenCalledWith({ kinds: ['service'], text: 'payments', limit: 50 });
    await vi.waitFor(() => expect(mira(target)).toBeTruthy());
    const a = mira(target) as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('https://mira.example/#/traces?q=service.name%3D%22payments%22&range=-1h');
    expect(a.getAttribute('rel')).toBe('noopener noreferrer');
    expect(a.getAttribute('target')).toBe('_blank');
    expect(text(a)).toBe('Open in Mira');
    done();
  });

  it('gives Resources no Mira link when the name is not unique', async () => {
    entitiesFn.mockResolvedValue({ entities: [svc('domain-a'), svc('domain-b')], truncated: false });
    const used = preview([{ offering: 'postgresql', consumer: ref('service', KEY, 'payments'), provider: ref('service', 'postgresql'), via: 'dependency',
      consumption: [{ state: 'not-reported', provenance: 'observed', reason: 'no provider reports usage' }] }]);
    const { target, done } = await render(detail({ consumedOfferings: used }), { miraURL: 'https://mira.example' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await Promise.resolve(); flushSync();
    expect(target.querySelector('[data-testid="resource-usage"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="resource-mira-link"]')).toBeNull();
    done();
  });

  it('links Mira from Resources when the name is unique', async () => {
    entitiesFn.mockResolvedValue({ entities: [svc('domain-a')], truncated: false });
    const used = preview([{ offering: 'postgresql', consumer: ref('service', KEY, 'payments'), provider: ref('service', 'postgresql'), via: 'dependency',
      consumption: [{ state: 'not-reported', provenance: 'observed', reason: 'no provider reports usage' }] }]);
    const { target, done } = await render(detail({ consumedOfferings: used }), { miraURL: 'https://mira.example' });
    await vi.waitFor(() => expect(target.querySelector('[data-testid="resource-mira-link"]')).toBeTruthy());
    expect(text(target.querySelector('[data-testid="resource-mira-link"]'))).toBe('Open payments in Mira');
    done();
  });

  it('gives no link when two domains share the name', async () => {
    entitiesFn.mockResolvedValue({ entities: [svc('domain-a'), svc('domain-b')], truncated: false });
    const { target, done } = await render(detail(), { miraURL: 'https://mira.example' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await Promise.resolve(); flushSync();
    expect(mira(target)).toBeNull();
    done();
  });

  it('gives no link when the answer is cut short or fails', async () => {
    entitiesFn.mockResolvedValueOnce({ entities: [svc('domain-a')], truncated: true });
    let r = await render(detail(), { miraURL: 'https://mira.example' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalledTimes(1));
    await Promise.resolve(); flushSync();
    expect(mira(r.target)).toBeNull();
    r.done();
    entitiesFn.mockRejectedValueOnce(new Error('down'));
    r = await render(detail(), { miraURL: 'https://mira.example' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalledTimes(2));
    await Promise.resolve(); flushSync();
    expect(mira(r.target)).toBeNull();
    r.done();
  });

  it('asks nothing without a Mira address', async () => {
    const { target, done } = await render(detail());
    expect(mira(target)).toBeNull();
    expect(entitiesFn).not.toHaveBeenCalled();
    done();
  });
});

describe('service page: an inferred service it replaced', () => {
  beforeEach(() => detailFn.mockReset());
  const replaced = {
    from: 'k8s:prod%2FDeployment%2Fexperiments', to: KEY, link: 'pacto-target',
    target: 'prod/Deployment/payments', at: '2026-11-04T09:56:00Z',
  };

  it('is one muted line among the facts, its details in the tooltip and no new heading', async () => {
    const plain = await render(detail());
    const headings = plain.target.querySelectorAll('h2, h3').length;
    expect(fact(plain.target, 'service-replaces')).toBeNull();
    plain.done();

    const { target, done } = await render(detail({ replaces: [replaced], replacesTotal: 1 }));
    const f = fact(target, 'service-replaces')!;
    expect(f.closest('.se-facts')).toBeTruthy();
    expect(text(f)).toContain('Replaces k8s:prod/Deployment/experiments · 4 minutes ago');
    const key = f.querySelector('bdi')!;
    expect(key.getAttribute('title')).toBe('k8s:prod/Deployment/experiments');
    expect(text(f.querySelector('[role="note"]'))).toBe('Linked by the Pacto target prod/Deployment/payments, which runs as the workload. The Pacto was created ' + new Date(replaced.at).toLocaleString('en-US', { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }) + '.');
    expect(target.querySelectorAll('h2, h3').length).toBe(headings);
    done();
  });

  it('counts the replacements the preview leaves out', async () => {
    const { target, done } = await render(detail({ replaces: [replaced], replacesTotal: 3 }));
    expect(text(fact(target, 'service-replaces'))).toContain('+2 more');
    done();
  });
});

describe('service page: asked for by the key of an inferred service it replaced', () => {
  beforeEach(() => detailFn.mockReset());

  it('renders the declared page with one notice line under the header', async () => {
    const replacedFrom = { from: 'k8s:prod%2FDeployment%2Fexperiments', to: KEY, link: 'pacto-target', target: 'prod/Deployment/payments', at: '2026-11-04T09:56:00Z' };
    const d = detail();
    const { target, done } = await render({ ...d, replacedFrom }, { entityKey: replacedFrom.from });
    const n = fact(target, 'entity-replaced-from')!;
    expect(text(n)).toBe('k8s:prod/Deployment/experiments was replaced by payments 4 minutes ago, linked by Pacto prod/Deployment/payments');
    expect(n.querySelector('bdi')?.getAttribute('title')).toBe('k8s:prod/Deployment/experiments');
    expect(target.querySelectorAll('[data-testid="entity-replaced-from"]')).toHaveLength(1);
    done();
  });

  it('shows no notice when the service was asked for by its own key', async () => {
    const { target, done } = await render(detail());
    expect(fact(target, 'entity-replaced-from')).toBeNull();
    done();
  });
});

describe('service page: inferred by Pacto', () => {
  beforeEach(() => detailFn.mockReset());
  const field = (i: number) => ({ field: `configuration.KEY_${i}`, value: 'string', source: `deploy/app.yaml:${i + 1}`, confidence: 'likely', provenance: 'inferred' });
  const fields = (n: number, extra = {}) => ({ inferredFields: preview(Array.from({ length: n }, (_, i) => field(i)), extra) });

  it('is one collapsed block with the total in its header', async () => {
    const { target, done } = await render(detail(fields(12)));
    const wrap = fact(target, 'service-inferred')!;
    const sec = wrap.querySelector('details') as HTMLDetailsElement;
    expect(sec.open).toBe(false);
    expect(text(sec.querySelector('summary'))).toContain('Inferred by Pacto');
    expect(text(sec.querySelector('summary'))).toContain('12 of 12');
    done();
  });

  it('opened with 12 items has exactly one disclosure for the rest', async () => {
    const { target, done } = await render(detail(fields(12)));
    const sec = fact(target, 'service-inferred')!.querySelector('details') as HTMLDetailsElement;
    sec.open = true;
    sec.dispatchEvent(new Event('toggle'));
    flushSync();
    expect(sec.querySelector('[data-testid="preview-show-all"]')).toBeNull();
    const inner = [...sec.querySelectorAll('.ps-panel details summary')].map((s) => text(s));
    expect(inner).toEqual(['7 more']);
    expect(sec.querySelector('button[aria-label="About inferred configuration.KEY_0"]')).toBeTruthy();
    done();
  });

  it('offers to accept every proposed range, only when a range is proposed', async () => {
    const proposal = { field: 'dependencies[ledger].compatibility', value: 'compatibility unknown', proposed: '^2.3.0', source: 'snapshot:reg/s@sha256:1', provenance: 'inferred' };
    const opened = async (items: unknown[]) => {
      const r = await render(detail({ inferredFields: preview(items) }));
      const sec = fact(r.target, 'service-inferred')!.querySelector('details') as HTMLDetailsElement;
      sec.open = true;
      sec.dispatchEvent(new Event('toggle'));
      flushSync();
      return r;
    };
    let r = await opened([field(0), proposal]);
    expect(text(fact(r.target, 'accept-proposed'))).toContain('Accept all proposed ranges for payments');
    r.done();
    // A proposed ref or an ordinary inferred field is no range to accept.
    r = await opened([field(0), { ...proposal, field: 'dependencies[ledger].ref', proposed: undefined }]);
    expect(fact(r.target, 'service-inferred')).toBeTruthy();
    expect(fact(r.target, 'accept-proposed')).toBeNull();
    r.done();
  });

  it('renders nothing when nothing is inferred', async () => {
    const { target, done } = await render(detail(fields(0)));
    expect(fact(target, 'service-inferred')).toBeNull();
    done();
  });

  it('says the record could not be read', async () => {
    const { target, done } = await render(detail(fields(0, { unreadable: true })));
    const sec = fact(target, 'service-inferred')!.querySelector('details') as HTMLDetailsElement;
    // Folded, the head already names the uncertainty: unknown, never absent.
    expect(sec.open).toBe(false);
    expect(text(sec.querySelector('summary')!)).toContain('Could not be read');
    expect(sec.dataset.tone).toBe('warn');
    sec.open = true;
    sec.dispatchEvent(new Event('toggle'));
    flushSync();
    expect(text(sec)).toContain('Pacto could not read the inferred record');
    done();
  });
});
