/**
 * Component tests for the Golden paths list: one row per service whose contract
 * declares service.template and that runs nowhere, with its owner, status, signature,
 * source and the services created from it.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetGoldenPathsView from './FleetGoldenPathsView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-11-07T10:00:00Z', completeness: 'complete', sources: [] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const listResp = (entities: any[], m: any = meta): any => ({ meta: m, total: entities.length, count: entities.length, offset: 0, limit: 25, truncated: false, entities });
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const pathRef = (name: string, extra: any = {}) => ({
  kind: 'service', key: `ghcr.io/acme/${name}`, label: name, href: `/fleet/services/${encodeURIComponent(`ghcr.io/acme/${name}`)}`,
  template: true, status: 'NotEvaluated', domain: 'ghcr.io/acme', secondary: 'ghcr.io/acme', ...extra,
});

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetGoldenPathsView, { target, props: { refreshTick: 0, ...props } });
  return { target, done: () => { unmount(component); document.body.removeChild(target); } };
}

beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

describe('FleetGoldenPathsView', () => {
  it('asks for golden paths only and lists each with its summary, owner, signature, source and created services', async () => {
    entitiesFn.mockResolvedValue(listResp([
      pathRef('http-service', { summary: 'An HTTP API on the platform runtime', owner: 'platform', signature: 'verified', created: 2 }),
      pathRef('worker', { signature: 'unsigned', created: 1 }),
      pathRef('cron', { signature: 'not-checked' }),
    ]));
    const { target, done } = mountView({ text: 'http' });
    await vi.waitFor(() => expect(target.querySelectorAll('[data-testid="golden-path-list"] tbody tr').length).toBe(3));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], template: 'only', text: 'http', limit: 25, sort: 'name' }));
    expect(target.querySelector('h1')?.textContent).toBe('Golden paths');
    const rows = target.querySelectorAll('[data-testid="golden-path-list"] tbody tr');
    expect(rows[0].textContent).toContain('http-service');
    expect(rows[0].textContent).toContain('An HTTP API on the platform runtime');
    expect(rows[0].textContent).toContain('platform');
    expect(rows[0].textContent).toContain('ghcr.io/acme');
    expect(rows[0].textContent).toContain('Verified');
    // A verified signature only names the signer the path's own contract declares.
    expect(rows[0].querySelector('[data-testid="path-signature"]')?.getAttribute('title')).toContain('its own contract declares');
    expect(rows[1].textContent).toContain('No owner declared');
    expect(rows[1].textContent).toContain('Unsigned');
    // No source that read it checks signatures: never shown as unknown.
    expect(rows[2].textContent).toContain('Not checked');
    const created = rows[0].querySelector('a[data-testid="path-created"]');
    expect(created?.textContent).toContain('2');
    expect(created?.getAttribute('href')).toBe('#/fleet/services?createdFrom=ghcr.io%2Facme%2Fhttp-service');
    expect(rows[0].querySelector('a.entity-link')?.getAttribute('href')).toBe('#/fleet/services/ghcr.io%2Facme%2Fhttp-service');
    done();
  });

  it('says what a golden path is when there is none', async () => {
    entitiesFn.mockResolvedValue(listResp([]));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.textContent).toContain('No golden paths yet: a golden path is a contract that declares service.template'));
    expect(target.querySelector('[data-testid="golden-paths-empty"] a')?.getAttribute('href')).toContain('pacto.run');
    done();
  });

  it('does not claim there is none when knowledge is incomplete', async () => {
    entitiesFn.mockResolvedValue(listResp([], { ...meta, completeness: 'partial' }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(target.textContent).not.toContain('No golden paths yet');
    done();
  });

  it('orders by health on request', async () => {
    entitiesFn.mockResolvedValue(listResp([pathRef('http-service')]));
    const { done } = mountView({ sort: 'health' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ sort: 'health', template: 'only' })));
    done();
  });
});
