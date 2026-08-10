import { describe, it, expect } from 'vitest';
import { buildCommands, flattenCommands } from './commands';

const services = [
  { name: 'payments-api', version: '1.2.0', owner: { team: 'team/payments' } },
  { name: 'checkout', version: '0.9.0', owner: { team: 'team/checkout' } },
];

describe('buildCommands', () => {
  it('empty query shows Views and Actions only', () => {
    const groups = buildCommands('', services);
    const labels = groups.map((g) => g.label);
    expect(labels).toEqual(['Views', 'Actions']);
    expect(groups[0].items.map((i) => i.label)).toContain('Graph');
  });

  it('filters services by name', () => {
    const groups = buildCommands('payments', services);
    const svc = groups.find((g) => g.label === 'Services');
    expect(svc?.items.map((i) => i.label)).toEqual(['payments-api']);
    expect(svc?.items[0].href).toBe('#/services/payments-api');
  });

  it('matches owners and dedupes them', () => {
    const groups = buildCommands('checkout', services);
    const owners = groups.find((g) => g.label === 'Owners');
    expect(owners?.items.map((i) => i.label)).toEqual(['team/checkout']);
  });

  it('filters views and actions by label', () => {
    const groups = buildCommands('theme', services);
    expect(groups.map((g) => g.label)).toEqual(['Actions']);
    expect(groups[0].items[0].action).toBe('theme');
  });

  it('flattenCommands preserves group order', () => {
    const flat = flattenCommands(buildCommands('', services));
    expect(flat[0].label).toBe('Services'); // first View is "Services" (href #/)
    expect(flat[flat.length - 1].action).toBe('autoreload');
  });
});

describe('buildCommands — capability-aware views (Part 1: one destination per concept)', () => {
  it('offers the PRODUCT destinations on a Fleet host and no legacy routes', () => {
    const groups = buildCommands('', [], true);
    const hrefs = (groups.find((g) => g.label === 'Views')?.items || []).map((v) => v.href);
    expect(hrefs).toContain('#/fleet');
    expect(hrefs).toContain('#/fleet/services');
    expect(hrefs).toContain('#/fleet/graph');
    expect(hrefs).toContain('#/fleet/owners');
    // Never the legacy roots on a Fleet host (no second door to a superseded UI).
    expect(hrefs).not.toContain('#/');
    expect(hrefs).not.toContain('#/graph');
    expect(hrefs).not.toContain('#/owners');
  });

  it('lists each Fleet destination once, and still finds it by the words a user types', () => {
    const views = buildCommands('', [], true).find((g) => g.label === 'Views')?.items || [];
    const hrefs = views.map((v) => v.href);
    expect(new Set(hrefs).size).toBe(hrefs.length); // no two rows opening the same screen
    // "compare" is the action, not a second destination -- it still finds the workspace.
    const found = buildCommands('compare', [], true).find((g) => g.label === 'Views')?.items || [];
    expect(found.map((v) => v.label)).toEqual(['Change analysis']);
  });

  /**
   * Readiness is not in the primary nav, so the palette is one of the few doors to it.
   * It opens the contract revision inventory -- the whole assessed population. It used
   * to open the Needs-attention readiness category, a triage cut that structurally
   * cannot contain a passing revision: typing "readiness" showed only the failures and
   * called them readiness.
   */
  it('sends Readiness to the whole revision population, not to the failure backlog', () => {
    const views = buildCommands('readiness', [], true).find((g) => g.label === 'Views')?.items || [];
    expect(views.map((v) => [v.label, v.href])).toEqual([['Readiness', '#/fleet/revisions']]);
  });

  it('offers the LEGACY destinations on a non-Fleet host (its only UI)', () => {
    const hrefs = (buildCommands('', [], false).find((g) => g.label === 'Views')?.items || []).map((v) => v.href);
    expect(hrefs).toContain('#/');
    expect(hrefs).toContain('#/graph');
    expect(hrefs).toContain('#/owners');
    expect(hrefs).not.toContain('#/fleet');
  });

  it('does not surface legacy service/owner search results on a Fleet host', () => {
    const groups = buildCommands('payments', services, true);
    // The visible EntitySearch handles product entity discovery on a Fleet host, so the
    // palette must not link to legacy service/owner detail routes.
    const svcGroup = groups.find((g) => g.label === 'Services' && g.items.some((i) => i.kind === 'service'));
    const ownerGroup = groups.find((g) => g.label === 'Owners');
    expect(svcGroup).toBeUndefined();
    expect(ownerGroup).toBeUndefined();
  });

  it('still surfaces legacy service search on a non-Fleet host', () => {
    const groups = buildCommands('payments', services, false);
    expect(groups.find((g) => g.label === 'Services' && g.items.some((i) => i.kind === 'service'))).toBeTruthy();
  });
});
