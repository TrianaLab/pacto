/**
 * Tests for the legacy->product migration view (Part 1). A legacy name-bearing URL is
 * resolved through the Product Entities API (never fabricating a canonical key): exactly
 * one match canonicalizes (replaces) the URL to the product entity; several matches show
 * an explicit disambiguation; none shows an honest not-found; a transport failure shows a
 * Product error state and NEVER falls back to the legacy screen.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import LegacyEntityRedirect from './LegacyEntityRedirect.svelte';
import { ApiError } from '../lib/api.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const ent = (key: string, label: string): any => ({ kind: 'service', key, label, href: `/fleet/services/${encodeURIComponent(key)}` });
const q = (t: HTMLElement, s: string) => t.querySelector(s);

async function mountRedirect(name: string) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(LegacyEntityRedirect, { target, props: { kind: 'service', name } });
  flushSync();
  await Promise.resolve(); await Promise.resolve();
  flushSync();
  return { target, component };
}

describe('LegacyEntityRedirect', () => {
  beforeEach(() => { entitiesFn.mockReset(); location.hash = '#/services/payments'; });

  it('canonicalizes (replaces) the URL to the single exact product match', async () => {
    entitiesFn.mockResolvedValue({ meta: {}, total: 1, count: 1, entities: [ent('domain-a/payments', 'payments')] });
    const { target, component } = await mountRedirect('payments');
    expect(location.hash).toBe('#/fleet/services/domain-a%2Fpayments');
    unmount(component); document.body.removeChild(target);
  });

  it('shows an explicit disambiguation for several same-named services (no redirect)', async () => {
    entitiesFn.mockResolvedValue({ meta: {}, total: 2, count: 2, entities: [ent('domain-a/payments', 'payments'), ent('domain-b/payments', 'payments')] });
    const { target, component } = await mountRedirect('payments');
    expect(q(target, '[data-testid="legacy-migration-ambiguous"]')).toBeTruthy();
    expect(location.hash).toBe('#/services/payments'); // unchanged; the user must choose
    unmount(component); document.body.removeChild(target);
  });

  it('shows an honest not-found when nothing matches the name', async () => {
    entitiesFn.mockResolvedValue({ meta: {}, total: 0, count: 0, entities: [] });
    const { target, component } = await mountRedirect('ghost');
    expect(q(target, '[data-testid="legacy-migration-notfound"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('ignores a fuzzy (non-exact) match rather than canonicalizing to the wrong entity', async () => {
    entitiesFn.mockResolvedValue({ meta: {}, total: 1, count: 1, entities: [ent('domain-a/payments-api', 'payments-api')] });
    const { target, component } = await mountRedirect('payments');
    expect(q(target, '[data-testid="legacy-migration-notfound"]')).toBeTruthy();
    expect(location.hash).toBe('#/services/payments');
    unmount(component); document.body.removeChild(target);
  });

  it('shows a Product error state on a transport failure, never the legacy screen', async () => {
    entitiesFn.mockRejectedValue(new ApiError(503, 'backend down'));
    const { target, component } = await mountRedirect('payments');
    expect(q(target, '[data-testid="legacy-migration-error"]')).toBeTruthy();
    expect(location.hash).toBe('#/services/payments');
    unmount(component); document.body.removeChild(target);
  });
});
