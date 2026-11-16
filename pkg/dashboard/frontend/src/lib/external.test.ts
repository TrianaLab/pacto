import { describe, it, expect } from 'vitest';
import { parseHash, fleetEntityUrl } from './router.ts';
import { kindLabel } from './entityLabels.ts';
import { fleetEntityBreadcrumbs } from './breadcrumbs.ts';
import { narrowEntityDetail } from './api.ts';

// The external entity page (the backend's /fleet/externals/<identity> href).
describe('external system routes', () => {
  it('round-trips an identity that holds slashes', () => {
    const key = 'aws/rds/123456789012/eu-west-1/orders-db';
    expect(fleetEntityUrl('external', key)).toBe(`#/fleet/externals/${encodeURIComponent(key)}`);
    expect(parseHash(fleetEntityUrl('external', key))).toEqual({ view: 'fleet-entity', params: { kind: 'external', key } });
    expect(parseHash('#/fleet/externals/stripe')).toEqual({ view: 'fleet-entity', params: { kind: 'external', key: 'stripe' } });
  });

  it('names the kind and roots the trail at Overview', () => {
    expect(kindLabel('external')).toBe('External system');
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'external', key: 'stripe', label: 'Stripe' } });
    expect(t.map((c) => c.label)).toEqual(['Overview', 'Stripe']);
  });

  it('narrows an external detail', () => {
    const raw = { entity: { kind: 'external', key: 'stripe', label: 'Stripe', href: '' }, external: { external: {}, consumers: [] } };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- a minimal payload
    expect(narrowEntityDetail(raw as any).external).toBeDefined();
  });
});
