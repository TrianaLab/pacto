import { describe, it, expect } from 'vitest';
import { graphBreadcrumbs, fleetEntityBreadcrumbs } from './breadcrumbs';

describe('graphBreadcrumbs', () => {
  it('is just Fleet with no group or focus', () => {
    expect(graphBreadcrumbs({ group: '', focus: '' })).toEqual([{ label: 'Fleet' }]);
  });

  it('links Fleet then the focused node', () => {
    expect(graphBreadcrumbs({ group: '', focus: 'payments' })).toEqual([
      { label: 'Fleet', href: '#/graph' },
      { label: 'payments' },
    ]);
  });

  it('shows the By owner crumb when grouped without focus', () => {
    expect(graphBreadcrumbs({ group: 'owner', focus: '' })).toEqual([
      { label: 'Fleet', href: '#/graph' },
      { label: 'By owner' },
    ]);
  });
});

describe('fleetEntityBreadcrumbs (H) — entity-relationship trails from canonical refs', () => {
  it('service: Fleet > Services > payments', () => {
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'service', key: 'domain-a/payments', label: 'payments' } });
    expect(t.map((c) => c.label)).toEqual(['Fleet', 'Services', 'payments']);
    expect(t[0].href).toBe('#/fleet');
    expect(t[1].href).toBe('#/fleet/services');
  });

  it('revision: uses the parent service REF (never the display string) and a Revision leaf', () => {
    const t = fleetEntityBreadcrumbs({
      entity: { kind: 'revision', key: 'domain-a/payments@2.1.0', label: 'payments 2.1.0' },
      revision: { version: '2.1.0', service: { kind: 'service', key: 'domain-a/payments', label: 'payments', href: '/fleet/services/domain-a%2Fpayments' } },
    });
    expect(t.map((c) => c.label)).toEqual(['Fleet', 'Services', 'payments', 'Revision 2.1.0']);
    // the parent-service crumb links to its canonical href from the ref, not an inferred path
    expect(t[2].href).toBe('#/fleet/services/domain-a%2Fpayments');
  });

  it('target: Fleet > Services > {service} > Deployment ...', () => {
    const t = fleetEntityBreadcrumbs({
      entity: { kind: 'target', key: 'prod/k8s/payments', label: 'prod/payments' },
      target: { service: { kind: 'service', key: 'domain-a/payments', label: 'payments', href: '/fleet/services/domain-a%2Fpayments' } },
    });
    expect(t.map((c) => c.label)).toEqual(['Fleet', 'Services', 'payments', 'Deployment prod/payments']);
  });

  it('owner: Fleet > Owners > platform-team', () => {
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'owner', key: 'platform-team', label: 'platform-team' } });
    expect(t.map((c) => c.label)).toEqual(['Fleet', 'Owners', 'platform-team']);
    expect(t[1].href).toBe('#/fleet/owners');
  });

  it('source: Fleet > Sources > kubernetes', () => {
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'source', key: 'kubernetes', label: 'kubernetes' } });
    expect(t.map((c) => c.label)).toEqual(['Fleet', 'Sources', 'kubernetes']);
    expect(t[1].href).toBe('#/fleet/sources');
  });

  it('falls back to just Fleet with no entity', () => {
    expect(fleetEntityBreadcrumbs(null).map((c) => c.label)).toEqual(['Fleet']);
  });
});
