import { describe, it, expect } from 'vitest';
import { parseHash, fleetOfferingsUrl, fleetEntityUrl, fleetServicesUrl, fleetEntityListUrl } from './router.ts';
import { kindLabel } from './entityLabels.ts';
import { fleetEntityBreadcrumbs } from './breadcrumbs.ts';
import { buildCommands, flattenCommands } from './commands.ts';
import { narrowEntityDetail, isOfferingDetail } from './api.ts';

describe('platform-offering routes', () => {
  it('parses the offering list and an offering page', () => {
    expect(parseHash('#/fleet/offerings')).toEqual({ view: 'fleet-offerings', params: {} });
    expect(parseHash('#/fleet/offerings?text=sql&offset=25&sort=services')).toEqual({
      view: 'fleet-offerings', params: { text: 'sql', offset: '25', sort: 'services' },
    });
    expect(parseHash('#/fleet/offerings/shared-config')).toEqual({ view: 'fleet-entity', params: { kind: 'offering', key: 'shared-config' } });
  });

  it('builds the offering list and offering page URLs', () => {
    expect(fleetOfferingsUrl()).toBe('#/fleet/offerings');
    expect(fleetOfferingsUrl({ text: 'sql', offset: 25, sort: 'services' })).toBe('#/fleet/offerings?text=sql&offset=25&sort=services');
    expect(fleetEntityUrl('offering', 'redis')).toBe('#/fleet/offerings/redis');
    expect(fleetEntityListUrl('offering')).toBe('#/fleet/offerings');
  });

  it('round-trips the consumes filter on services', () => {
    const url = fleetServicesUrl({ consumes: 'postgresql' });
    expect(url).toBe('#/fleet/services?consumes=postgresql');
    expect(parseHash(url)).toEqual({ view: 'fleet-services', params: { consumes: 'postgresql' } });
  });

  it('round-trips the offering filter on services', () => {
    const url = fleetServicesUrl({ offering: 'postgresql' });
    expect(url).toBe('#/fleet/services?offering=postgresql');
    expect(parseHash(url)).toEqual({ view: 'fleet-services', params: { offering: 'postgresql' } });
  });
});

describe('platform-offering labels and navigation', () => {
  it('names the kind', () => {
    expect(kindLabel('offering')).toBe('Platform offering');
  });

  it('breadcrumbs: Overview > Offerings > redis', () => {
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'offering', key: 'redis', label: 'redis' } });
    expect(t.map((c) => c.label)).toEqual(['Overview', 'Offerings', 'redis']);
    expect(t[1].href).toBe('#/fleet/offerings');
  });

  it('the command palette finds the offerings list', () => {
    const cmds = flattenCommands(buildCommands('platform', [], true));
    expect(cmds.find((c) => c.label === 'Platform offerings')?.href).toBe('#/fleet/offerings');
    expect(flattenCommands(buildCommands('provider', [], true)).map((c) => c.label)).toContain('Platform offerings');
  });

  it('narrows an offering detail', () => {
    const raw = { entity: { kind: 'offering', key: 'redis', label: 'redis', href: '' }, offering: { providers: {}, consumers: {} } };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- a minimal payload
    const d = narrowEntityDetail(raw as any);
    expect(isOfferingDetail(d)).toBe(true);
  });
});
