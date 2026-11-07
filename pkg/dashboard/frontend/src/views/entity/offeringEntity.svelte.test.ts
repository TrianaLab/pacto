/**
 * The platform-offering page: one card per provider with its owner, versions,
 * interfaces, configuration and the consume snippet, then the services that consume it.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { detailFn, versionsFn } = vi.hoisted(() => ({ detailFn: vi.fn(), versionsFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetEntityDetail: (...a: unknown[]) => detailFn(...a), fleetVersions: (...a: unknown[]) => versionsFn(...a) } };
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
    versions: { ...preview([{ revision: r2, version: '1.0.1', inUse: true }, { revision: r1, version: '1.0.0', inUse: false }]), completeness: 'fleet', note: 'Versions come from the fleet.' },
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

beforeEach(() => { detailFn.mockReset(); versionsFn.mockReset(); });

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
    expect(c.querySelectorAll('.ov-chips li').length).toBe(2);
    expect(text(c)).not.toContain('in use: not shown');
    expect(target.querySelector('h1')?.textContent).toContain('shared-config');
    done();
  });

  it('marks deprecated and past-sunset versions, says nothing of use it cannot see and counts the rest', async () => {
    const svc = ref('service', 'ghcr.io/acme/postgresql', 'postgresql');
    const v = (version: string, extra: Record<string, unknown> = {}) => ({ revision: ref('revision', `${svc.key}@sha256:${version}`, `postgresql ${version}`), version, ...extra });
    const versions = { ...preview([
      v('16.1.0'), v('16.0.0', { lifecycle: { deprecated: '2026-01-01', sunset: '2999-01-01' } }), v('15.0.0', { lifecycle: { deprecated: '2025-01-01', sunset: '2025-06-01' }, sunsetReached: true }),
    ], { total: 5, count: 3, truncated: true }), completeness: 'fleet', note: '' };
    const { target, done } = await render(detail({ providers: preview([provider('postgresql', { service: svc, versions })]) }));
    const chips = Array.from(target.querySelectorAll('.ov-chips li')) as HTMLElement[];
    expect(chips.map(text)).toEqual(['16.1.0', '16.0.0 Deprecated', '15.0.0 Past sunset', '+2 more']);
    expect(text(target.querySelector('[data-testid="offering-provider"]'))).toContain('in use: not shown');
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

  it('says what Pacto validates and shows, and links the guide', async () => {
    const { target, done } = await render(detail({}));
    expect(text(target)).toContain("Pacto validates what consumers request and shows what providers report. Provisioning runs in your platform's tools.");
    expect(target.querySelector('.oe-foot a')?.getAttribute('href')).toBe('https://pacto.run/latest/provision-infrastructure/');
    expect(target.querySelector('[data-testid="hidden-consumers"]')).toBeNull();
    expect(text(target)).toContain('No service has a resolved declaration that consumes this offering. References that are not digest-pinned or locked cannot be resolved and are not counted.');
    done();
  });

  it('marks an observed membership and the provider lifecycle', async () => {
    const { target, done } = await render(detail({ providers: preview([
      provider('a', { offeringProvenance: 'observed', lifecycle: { deprecated: '2026-01-01', sunset: '2999-01-01' } }),
      provider('b', { offeringProvenance: 'declared' }),
    ]) }));
    const [a, b] = Array.from(target.querySelectorAll('[data-testid="offering-provider"] .oe-head')) as HTMLElement[];
    expect(text(a)).toContain('observed');
    expect(text(a)).toContain('Its targets report this offering. Its contract declares none.');
    expect(text(a)).toContain('Deprecated');
    expect(text(b)).not.toContain('observed');
    done();
  });

  it('checks a range against the server and shows the latest compatible version', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      const p = provider('postgresql');
      const [r2, r1] = (p.versions.items as Array<{ revision: unknown }>).map((v) => v.revision);
      versionsFn.mockResolvedValue({ providers: [{ provider: p.service, versions: { ...preview([
        { revision: r2, version: '1.0.1', inUse: true, rangeStatus: 'unsatisfied' }, { revision: r1, version: '1.0.0', inUse: false, rangeStatus: 'satisfied' },
      ]), latestCompatible: r1, range: '~1.0.0', completeness: 'fleet', note: 'Versions come from the contract revisions this snapshot holds.' } }] });
      const { target, done } = await render(detail({ providers: preview([p]) }));
      const input = target.querySelector('input[type="text"]') as HTMLInputElement;
      expect(input.closest('label')?.textContent).toContain('Versions satisfying');
      input.value = '~1.0.0';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.value = ' ~1.0.0 ';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      await vi.advanceTimersByTimeAsync(300);
      expect(versionsFn).toHaveBeenCalledTimes(1);
      expect(versionsFn).toHaveBeenCalledWith({ offering: 'shared-config', provider: p.service.key, range: '~1.0.0' });
      flushSync();
      const chips = Array.from(target.querySelectorAll('.ov-chips li')) as HTMLElement[];
      expect(chips.map(text)).toEqual(['1.0.1 in use unsatisfied', '1.0.0 satisfied']);
      expect(text(target.querySelector('.ov'))).toContain('Latest compatible 1.0.0');
      expect(text(target.querySelector('.ov'))).toContain('Versions come from the contract revisions this snapshot holds.');

      versionsFn.mockResolvedValue({ providers: [{ provider: p.service, versions: { ...preview([]), completeness: 'fleet', note: '' } }] });
      input.value = '^9.0.0';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      await vi.advanceTimersByTimeAsync(300);
      flushSync();
      expect(text(target.querySelector('.ov'))).toContain('No version satisfies ^9.0.0');

      versionsFn.mockRejectedValue(new Error('invalid query: range "nope": not a semver range'));
      input.value = 'nope';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      await vi.advanceTimersByTimeAsync(300);
      flushSync();
      expect(target.querySelector('[role="alert"]')?.textContent).toBe('invalid query: range "nope": not a semver range');

      versionsFn.mockRejectedValue({});
      input.value = 'x';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      await vi.advanceTimersByTimeAsync(300);
      flushSync();
      expect(target.querySelector('[role="alert"]')?.textContent).toBe('The range could not be checked.');

      input.value = '';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      flushSync();
      expect(target.querySelector('[role="alert"]')).toBeNull();
      expect(target.querySelectorAll('.ov-chips li').length).toBe(2);
      expect(text(target.querySelector('.ov'))).not.toContain('Latest compatible');
      done();
    } finally {
      vi.useRealTimers();
    }
  });

  it('drops an answer a newer range replaced', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      let resolveFirst: (v: unknown) => void = () => {};
      versionsFn.mockImplementationOnce(() => new Promise((r) => { resolveFirst = r; }));
      const { target, done } = await render(detail({ providers: preview([provider('a')]) }));
      const input = target.querySelector('input[type="text"]') as HTMLInputElement;
      input.value = '^1.0.0';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      await vi.advanceTimersByTimeAsync(300);
      input.value = '';
      input.dispatchEvent(new Event('input', { bubbles: true }));
      resolveFirst({ providers: [] });
      await vi.advanceTimersByTimeAsync(0);
      flushSync();
      expect(text(target.querySelector('.ov'))).not.toContain('Latest compatible');
      done();
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows a provisioning badge per environment with the detail in its title, and the provider lifecycle', async () => {
    const pa = provider('postgresql').service;
    const rev = ref('revision', 'ghcr.io/acme/catalog@sha256:3', 'catalog 1.3.0');
    const use = (consumer: string, provisioning: unknown[], extra: Record<string, unknown> = {}) => ({ offering: 'shared-config', consumer: ref('service', consumer), provider: pa, via: 'configuration_reference', provisioning, ...extra });
    const { target, done } = await render(detail({ consumers: preview([
      use('catalog', [
        { environment: 'production', phase: 'Ready', reportedPhase: 'Ready', provenance: 'observed', verified: true, producer: 'platform-adapter', source: 'evidence', sourceKind: 'evidence-http', provider: 'postgresql', externalRef: 'db-catalog-production-' + 'x'.repeat(200), asOf: '2026-11-04T09:00:00Z', revision: rev },
        { environment: 'staging', phase: 'Unknown', provenance: 'observed', verified: false, conflict: ['Failed', 'Ready'] },
      ], { providerLifecycle: { deprecated: '2026-01-01', sunset: '2999-01-01' } }),
      use('orders', [{ phase: 'Unknown', provenance: 'observed', verified: false, reason: 'no provider has reported provisioning for this use' }]),
    ]) }));
    const [catalog, orders] = Array.from(target.querySelectorAll('[data-testid="offering-consumer"]')) as HTMLElement[];
    const badges = Array.from(catalog.querySelectorAll('[data-testid="provisioning-badge"] .tag')) as HTMLElement[];
    expect(badges.map((b) => b.textContent)).toEqual(['production: Ready', 'staging: Unknown']);
    expect(badges[0].classList.contains('tone-ok')).toBe(true);
    expect(badges[1].classList.contains('tone-neutral')).toBe(true);
    const title = badges[0].getAttribute('title') ?? '';
    expect(title).toMatch(/^Ready \(reported by platform-adapter through evidence, /);
    expect(title).toContain('External reference: db-catalog-production-');
    expect(title).toContain('Source: evidence-http');
    expect(title).toContain('Consumer revision: catalog 1.3.0');
    expect(badges[1].getAttribute('title')).toBe('Unknown: reporters disagree (Failed, Ready)');
    expect(text(catalog)).toContain('Deprecated');
    expect(text(orders.querySelector('[data-testid="provisioning-badge"] .tag'))).toBe('Unknown');
    expect(orders.querySelector('[data-testid="provisioning-badge"] .tag')?.getAttribute('title')).toBe('Unknown: no provider has reported provisioning for this use');
    done();
  });

  it('counts the consumers it hides and links every consumer', async () => {
    const pa = provider('a').service;
    const many = Array.from({ length: 3 }, (_, i) => ({ offering: 'shared-config', consumer: ref('service', `c${i}`), provider: pa, via: 'dependency' }));
    const { target, done } = await render(detail({ consumers: preview(many, { total: 1000, truncated: true }), hiddenConsumers: 7 }));
    expect(text(target.querySelector('[data-testid="hidden-consumers"]'))).toBe('7 consumers you may not see');
    const all = target.querySelector('a[href="#/fleet/services?consumes=shared-config"]');
    expect(all?.textContent).toBe('View all consumers');
    done();
  });

  it('counts a single hidden consumer even when none is visible', async () => {
    const { target, done } = await render(detail({ hiddenConsumers: 1 }));
    expect(text(target.querySelector('[data-testid="hidden-consumers"]'))).toBe('1 consumer you may not see');
    done();
  });
});
