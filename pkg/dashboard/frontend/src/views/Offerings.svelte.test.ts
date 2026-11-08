/**
 * Component tests for the platform-offering list: one row per service.offering with
 * its provider and target counts, and the sentence that says how to declare one.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetOfferingsView from './FleetOfferingsView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-11-04T10:00:00Z', completeness: 'complete', sources: [] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const listResp = (entities: any[], m: any = meta): any => ({ meta: m, total: entities.length, count: entities.length, offset: 0, limit: 25, truncated: false, entities });
const offeringRef = (name: string, services: number, targets: number) => (
  { kind: 'offering', key: name, label: name, offering: name, services, targets, href: `/fleet/offerings/${name}` });

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetOfferingsView, { target, props: { refreshTick: 0, ...props } });
  return { target, done: () => { unmount(component); document.body.removeChild(target); } };
}

beforeEach(() => { entitiesFn.mockReset(); location.hash = ''; });

describe('FleetOfferingsView', () => {
  it('lists offerings with their providers and operational targets', async () => {
    entitiesFn.mockResolvedValue(listResp([offeringRef('postgresql', 1, 3), offeringRef('shared-config', 2, 0)]));
    const { target, done } = mountView({ text: 'sql' });
    await vi.waitFor(() => expect(target.querySelectorAll('[data-testid="offering-list"] .lv-item').length).toBe(2));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['offering'], text: 'sql', limit: 25 }));
    expect(target.querySelector('h1')?.textContent).toBe('Platform offerings');
    expect(target.textContent).toContain('Services that platform teams publish for other teams to use, grouped by the service.offering their contracts declare.');
    const rows = target.querySelectorAll('[data-testid="offering-list"] .lv-item');
    expect(rows[0].textContent).toContain('1 provider · 3 operational targets');
    expect(rows[1].textContent).toContain('2 providers · 0 operational targets');
    expect(Array.from(target.querySelectorAll('[data-testid="offering-list"] a.entity-link')).map((a) => a.getAttribute('href')))
      .toEqual(['#/fleet/offerings/postgresql', '#/fleet/offerings/shared-config']);
    done();
  });

  it('explains how to declare an offering when none is declared', async () => {
    entitiesFn.mockResolvedValue(listResp([]));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.textContent).toContain('No contract declares a platform offering yet.'));
    expect(target.textContent).toContain('Set service.offering in a contract (pactoVersion 2.4) to list it here.');
    done();
  });

  it('does not claim none is declared when knowledge is incomplete', async () => {
    entitiesFn.mockResolvedValue(listResp([], { ...meta, completeness: 'partial' }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(target.textContent).not.toContain('No contract declares a platform offering yet.');
    done();
  });
});
