/**
 * The service page of a golden path (its plugins, the pinned command that creates a
 * service from it, its self-declared signature, its requirements and the services
 * created from it) and of a service created from one (its declared origin and the
 * derived creation checklist, where Unknown never reads as done).
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { detailFn, entitiesFn } = vi.hoisted(() => ({ detailFn: vi.fn(), entitiesFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a), fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from '../FleetEntityView.svelte';

const PATH = 'ghcr.io/acme/http-service';
const preview = (items: unknown[], extra: Record<string, unknown> = {}) => ({
  total: items.length, count: items.length, truncated: false, items, ...extra,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const ref = (kind: string, key: string, label?: string, extra: any = {}): any => ({ kind, key, label: label ?? key, href: `/fleet/${kind}s/${encodeURIComponent(key)}`, ...extra });

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function detail(key: string, label: string, service: Record<string, any> = {}, entity: Record<string, any> = {}): any {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 's', asOf: '2026-11-07T10:00:00Z', completeness: 'complete', sources: [{ id: 'oci', kind: 'oci', status: 'available' }] },
    entity: ref('service', key, label, entity),
    actions: [],
    service: {
      summary: { targets: 0, revisions: 1, declaredDependencies: 0 },
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

const CLI = `pacto init <folder> --from oci://${PATH}@sha256:${'a'.repeat(64)} --owner <team>`;
const MCP = `pacto_create from="oci://${PATH}@sha256:${'a'.repeat(64)}"`;
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const template = (extra: Record<string, any> = {}) => ({
  provenance: 'declared',
  plugins: [
    { name: 'dockerfile', description: 'A Dockerfile for a Go HTTP service' },
    { name: 'github-actions', description: 'A CI workflow', options: { 'go-version': '1.27' } },
  ],
  requirements: preview([{ offering: 'postgresql', consumer: ref('service', PATH, 'http-service'), provider: ref('service', 'ghcr.io/acme/postgresql', 'postgresql'), via: 'dependency', rangeStatus: 'satisfied', compatibility: '^1.0.0' }]),
  unresolvedRequirements: preview([{ ref: 'oci://ghcr.io/acme/http-baseline', reason: 'no service matches it' }]),
  createdServices: preview([ref('service', 'ghcr.io/acme/gift-cards', 'gift-cards')]),
  hiddenCreatedServices: 2,
  signature: { status: 'verified', signer: 'platform@acme.dev', issuer: 'https://accounts.example', bundles: 1 },
  signatureNote: 'Signed by the signer the golden path\'s own contract declares; Pacto did not check it against a trust config.',
  instantiate: { ref: `oci://${PATH}@sha256:${'a'.repeat(64)}`, cli: CLI, mcp: MCP },
  ...extra,
});

async function render(d: unknown) {
  detailFn.mockResolvedValue(d);
  entitiesFn.mockResolvedValue({ entities: [], total: 0, truncated: false });
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind: 'service', entityKey: 'x', refreshTick: 0 } });
  await vi.waitFor(() => expect(target.querySelector('.svc-entity')).toBeTruthy());
  flushSync();
  return { target, done: () => { unmount(component); target.remove(); } };
}

beforeEach(() => { detailFn.mockReset(); entitiesFn.mockReset(); });

describe('ServiceEntity: a golden path', () => {
  it('shows the badge, the plugins, the pinned command, the signature, the requirements and the created services', async () => {
    const { target, done } = await render(detail(PATH, 'http-service', { template: template() }, { template: true, summary: 'An HTTP API on the platform runtime' }));
    const gp = target.querySelector('[data-testid="golden-path"]')!;
    expect(gp).toBeTruthy();
    expect(gp.textContent).toContain('Golden path');
    expect(gp.textContent).toContain('An HTTP API on the platform runtime');
    const plugins = Array.from(gp.querySelectorAll('[data-testid="golden-path-plugins"] tbody tr')).map((r) => r.textContent);
    expect(plugins).toHaveLength(2);
    expect(plugins[0]).toContain('dockerfile');
    expect(plugins[1]).toContain('go-version');
    expect(plugins[1]).toContain('1.27');
    const create = gp.querySelector('[data-testid="golden-path-create"]')!;
    expect(create.textContent).toContain(CLI);
    expect(create.textContent).toContain(MCP);
    expect(create.querySelectorAll('button.copyable-btn').length).toBe(2);
    const sig = gp.querySelector('[data-testid="golden-path-signature"]')!;
    expect(sig.textContent).toContain('platform@acme.dev');
    expect(sig.textContent).toContain('did not check it against a trust config');
    const req = gp.querySelector('[data-testid="golden-path-requirements"]')!;
    expect(req.textContent).toContain('postgresql');
    expect(req.textContent).toContain('satisfied');
    expect(req.textContent).toContain('oci://ghcr.io/acme/http-baseline');
    const created = target.querySelector('[data-testid="golden-path-created"]')!;
    expect(created.textContent).toContain('gift-cards');
    expect(created.textContent).toContain('2 services you may not see');
    expect(Array.from(created.querySelectorAll('a')).map((a) => a.getAttribute('href')))
      .toContain('#/fleet/services?createdFrom=ghcr.io%2Facme%2Fhttp-service');
    done();
  });

  it('says why there is no command when the path has no digest-pinned reference', async () => {
    const t = template({ instantiate: undefined, instantiateReason: 'no digest-pinned reference: publish the golden path to get a copyable command' });
    const { target, done } = await render(detail(PATH, 'http-service', { template: t }, { template: true }));
    const create = target.querySelector('[data-testid="golden-path-create"]')!;
    expect(create.textContent).toContain('no digest-pinned reference: publish the golden path to get a copyable command');
    expect(create.querySelectorAll('button.copyable-btn').length).toBe(0);
    done();
  });

  it('says Not checked, with no signer note, when no source checks signatures', async () => {
    const { target, done } = await render(detail(PATH, 'http-service', { template: template({ signature: undefined }) }, { template: true }));
    const sig = target.querySelector('[data-testid="golden-path-signature"]')!;
    expect(sig.textContent).toContain('Not checked');
    expect(sig.textContent).not.toContain('did not check it against a trust config');
    done();
  });

  it('counts the requirements on services the viewer may not see', async () => {
    const t = template({ requirements: preview([]), unresolvedRequirements: preview([]), hiddenRequirements: 1 });
    const { target, done } = await render(detail(PATH, 'http-service', { template: t }, { template: true }));
    const req = target.querySelector('[data-testid="golden-path-requirements"]')!;
    expect(req.textContent).toContain('1 requirement on services you may not see.');
    expect(req.textContent).not.toContain('It consumes no offering.');
    done();
  });

  it('says a local folder command copies what the folder holds when it runs', async () => {
    const t = template({ instantiate: { ref: '/paths/http-service', cli: 'pacto init <name> --from /paths/http-service --owner <team>', mcp: 'x' } });
    const { target, done } = await render(detail(PATH, 'http-service', { template: t }, { template: true }));
    const create = target.querySelector('[data-testid="golden-path-create"]')!;
    expect(create.textContent).toContain('names a local folder');
    expect(create.textContent).not.toContain('by digest');
    done();
  });

  it('links no drift row of a golden path to an attention list it never appears in', async () => {
    const summary = { targets: 0, revisions: 1, declaredDependencies: 1, unresolvedDeclared: 1, unsatisfiedRanges: 1 };
    const { target, done } = await render(detail(PATH, 'http-service', { template: template(), summary }, { template: true }));
    const drift = target.querySelector('.se-drift')!;
    expect(drift.textContent).toContain('Unresolved declarations');
    expect(Array.from(drift.querySelectorAll('a')).map((a) => a.getAttribute('href')).filter((h) => h?.includes('attention'))).toEqual([]);
    done();
  });

  it('shows nothing of the sort on an ordinary service', async () => {
    const { target, done } = await render(detail('ghcr.io/acme/payments', 'payments'));
    expect(target.querySelector('[data-testid="golden-path"]')).toBeNull();
    expect(target.querySelector('[data-testid="service-origin"]')).toBeNull();
    expect(target.querySelector('[data-testid="creation-status"]')).toBeNull();
    done();
  });
});

describe('ServiceEntity: a service created from a golden path', () => {
  const origin = {
    ref: `oci://${PATH}:1.0.0`, digest: `sha256:${'b'.repeat(64)}`, provenance: 'declared',
    goldenPath: ref('service', PATH, 'http-service'), revision: ref('revision', `${PATH}@sha256:${'c'.repeat(64)}`, 'http-service 1.0.0'),
  };
  const creation = [
    { step: 'signature', status: 'unknown', detail: 'the golden path is unsigned' },
    { step: 'contract', status: 'done', detail: 'service.origin names the golden path' },
    { step: 'published', status: 'done', detail: 'read from a registry' },
    { step: 'deployed', status: 'unknown', detail: 'no operational target observed' },
    { step: 'provisioning', status: 'not-applicable', detail: 'consumes no offering' },
  ];

  it('shows the declared origin with a link to the golden path and the creation checklist', async () => {
    const { target, done } = await render(detail('ghcr.io/acme/gift-cards', 'gift-cards', { origin, creation }));
    const o = target.querySelector('[data-testid="service-origin"]')!;
    expect(o.textContent).toContain('Created from');
    expect(o.textContent).toContain('declared');
    expect(o.querySelector('a.entity-link')?.getAttribute('href')).toBe('#/fleet/services/ghcr.io%2Facme%2Fhttp-service');
    const rows = Array.from(target.querySelectorAll('[data-testid="creation-status"] li'));
    expect(rows.map((r) => r.getAttribute('data-step'))).toEqual(['signature', 'contract', 'published', 'deployed', 'provisioning']);
    const deployed = rows[3];
    expect(deployed.textContent).toContain('Deployed');
    expect(deployed.textContent).toContain('Unknown');
    expect(deployed.textContent).toContain('no operational target observed');
    // Unknown is styled as the Unknown knowledge state, never as done.
    expect(deployed.querySelector('.tag')?.className).not.toContain('tone-ok');
    expect(rows[1].querySelector('.tag')?.className).toContain('tone-ok');
    done();
  });

  it('gives the reason when the origin does not resolve', async () => {
    const unresolved = { ref: 'oci://ghcr.io/acme/gone:1.0.0', digest: `sha256:${'d'.repeat(64)}`, provenance: 'declared', reason: 'no golden path revision has this digest' };
    const { target, done } = await render(detail('ghcr.io/acme/gift-cards', 'gift-cards', { origin: unresolved }));
    const o = target.querySelector('[data-testid="service-origin"]')!;
    expect(o.textContent).toContain('oci://ghcr.io/acme/gone:1.0.0');
    expect(o.textContent).toContain('no golden path revision has this digest');
    expect(o.querySelector('a.entity-link')).toBeNull();
    done();
  });
});
