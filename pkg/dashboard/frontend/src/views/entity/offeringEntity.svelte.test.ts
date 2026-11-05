/**
 * The platform-offering page: one card per provider with its owner, versions,
 * interfaces, configuration and the consume snippet, then the services that consume it.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { detailFn } = vi.hoisted(() => ({ detailFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from '../FleetEntityView.svelte';

const preview = (items: unknown[], extra: Record<string, unknown> = {}) => ({ total: items.length, count: items.length, truncated: false, items, ...extra });
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
const ref = (kind: string, key: string, label?: string, extra: any = {}): any => ({ kind, key, label: label ?? key, href: `/fleet/${kind}s/${encodeURIComponent(key)}`, ...extra });
const YAML = 'configurations:\n  - name: platform-app-config\n    ref: oci://ghcr.io/acme/platform-app-config\n    required: true\n';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function provider(name: string, extra: Record<string, any> = {}) {
  const svc = ref('service', `ghcr.io/acme/${name}`, name);
  const r1 = ref('revision', `${svc.key}@sha256:1`, `${name} 1.0.0`);
  const r2 = ref('revision', `${svc.key}@sha256:2`, `${name} 1.0.1`);
  return {
    service: svc,
    ownership: { declared: true, owner: 'team:platform', ref: ref('owner', 'team:platform') },
    summary: `Shared settings from ${name}.`,
    currentRevision: r2,
    revisions: preview([r2, r1]),
    activeRevisions: preview([r2]),
    interfaces: { ...preview([{ name: 'api', type: 'http' }]), deprecated: 0, pastSunset: 0 },
    configurations: preview([]),
    policies: preview([]),
    consume: { ref: `oci://${svc.key}`, dependency: false, configuration: true, policy: false, yaml: YAML },
    ...extra,
  };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function detail(offering: Record<string, any>): any {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 's', asOf: '2026-11-04T10:00:00Z', completeness: 'complete', sources: [] },
    entity: ref('offering', 'shared-config', 'shared-config', { href: '/fleet/offerings/shared-config', offering: 'shared-config' }),
    actions: [],
    offering: { providers: preview([]), consumers: preview([]), ...offering },
  };
}

async function render(d: unknown) {
  detailFn.mockResolvedValue(d);
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind: 'offering', entityKey: 'shared-config', refreshTick: 0 } });
  await vi.waitFor(() => expect(target.querySelector('.offering-entity')).toBeTruthy());
  flushSync();
  return { target, done: () => { unmount(component); target.remove(); } };
}
const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();

beforeEach(() => detailFn.mockReset());

describe('offering page', () => {
  it('renders a card per provider with its owner, summary, versions and interfaces', async () => {
    const { target, done } = await render(detail({ providers: preview([provider('platform-app-config'), provider('platform-worker-config')]) }));
    const cards = target.querySelectorAll('[data-testid="offering-provider"]');
    expect(cards.length).toBe(2);
    const c = cards[0] as HTMLElement;
    expect(text(c)).toContain('platform-app-config');
    expect(text(c)).toContain('Shared settings from platform-app-config.');
    expect(text(c)).toContain('in use');
    expect(text(c)).toContain('api');
    expect(c.querySelectorAll('.oe-chips li').length).toBe(2);
    expect(target.querySelector('h1')?.textContent).toContain('shared-config');
    done();
  });

  it('copies the exact snippet YAML and captions it', async () => {
    const { target, done } = await render(detail({ providers: preview([provider('platform-app-config')]) }));
    const c = target.querySelector('[data-testid="consume"]') as HTMLElement;
    expect(c.querySelector('pre.consume-snippet code')?.textContent).toBe(YAML);
    expect(text(c)).toContain('Add this to your contract to consume shared-config from platform-app-config.');
    const btn = c.querySelector('button') as HTMLButtonElement;
    expect(btn.getAttribute('aria-label')).toBe('Copy consume snippet');
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    btn.click();
    expect(writeText).toHaveBeenCalledWith(YAML);
    done();
  });

  it('says how to get a snippet when the provider has no registry reference', async () => {
    const { target, done } = await render(detail({ providers: preview([provider('local-only', { consume: undefined })]) }));
    const c = target.querySelector('[data-testid="consume"]') as HTMLElement;
    expect(text(c)).toBe('Publish this provider to a registry to get a snippet.');
    expect(c.querySelector('pre')).toBeNull();
    done();
  });

  it('lists consumers with how they consume and their range status', async () => {
    const pa = provider('platform-app-config').service;
    const use = (consumer: string, via: string, rangeStatus?: string) => ({ offering: 'shared-config', consumer: ref('service', consumer), provider: pa, via, rangeStatus });
    const { target, done } = await render(detail({ consumers: preview([
      use('payments', 'dependency', 'satisfied'), use('orders', 'dependency', 'unsatisfied'), use('fraud', 'dependency', 'unknown'), use('cart', 'configuration_reference'),
    ]) }));
    const rows = Array.from(target.querySelectorAll('[data-testid="offering-consumer"]')) as HTMLElement[];
    expect(rows.length).toBe(4);
    expect(text(rows[0])).toContain('dependency');
    expect(rows[0].querySelector('.tone-ok')?.textContent).toBe('satisfied');
    expect(rows[1].querySelector('.tone-warn')?.textContent).toBe('unsatisfied');
    expect(rows[2].querySelector('.tone-neutral:last-of-type')?.textContent).toBe('unknown');
    expect(text(rows[3])).toContain('configuration');
    done();
  });

  it('says how many providers a truncated list leaves out', async () => {
    const { target, done } = await render(detail({ providers: preview([provider('a')], { total: 30, count: 1, truncated: true }) }));
    expect(text(target)).toContain('+29 more providers');
    expect(target.querySelector('a[href="#/fleet/services?offering=shared-config"]')).toBeTruthy();
    done();
  });

  it('says the page is read from contracts', async () => {
    const { target, done } = await render(detail({}));
    expect(text(target)).toContain("Pacto lists what contracts declare. Provisioning stays in your platform's tools.");
    expect(text(target)).toContain('No service has a resolved declaration that consumes this offering. References that are not digest-pinned or locked cannot be resolved and are not counted.');
    done();
  });
});
