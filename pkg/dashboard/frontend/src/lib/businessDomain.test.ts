import { describe, it, expect } from 'vitest';
import {
  parseHash, fleetDomainsUrl, fleetEntityUrl, fleetServicesUrl, fleetEntityListUrl, fleetAttentionUrl,
} from './router.ts';
import {
  kindLabel, ATTENTION_CATEGORIES, attentionCategoryLabel, businessDomainLabel,
  targetEnvironmentLabel, targetEnvironmentTone,
} from './entityLabels.ts';
import { identityContext } from './identityContext.ts';
import { fleetEntityBreadcrumbs } from './breadcrumbs.ts';

describe('business-domain routes', () => {
  it('parses the domain list and a domain page', () => {
    expect(parseHash('#/fleet/domains')).toEqual({ view: 'fleet-domains', params: {} });
    expect(parseHash('#/fleet/domains?text=pay&offset=25&sort=services')).toEqual({
      view: 'fleet-domains', params: { text: 'pay', offset: '25', sort: 'services' },
    });
    expect(parseHash('#/fleet/domains/payments')).toEqual({ view: 'fleet-entity', params: { kind: 'domain', key: 'payments' } });
  });

  it('builds the domain list and domain page URLs', () => {
    expect(fleetDomainsUrl()).toBe('#/fleet/domains');
    expect(fleetDomainsUrl({ text: 'pay', offset: 25, sort: 'services' })).toBe('#/fleet/domains?text=pay&offset=25&sort=services');
    expect(fleetEntityUrl('domain', 'payments')).toBe('#/fleet/domains/payments');
    expect(fleetEntityListUrl('domain')).toBe('#/fleet/domains');
  });

  it('round-trips the business domain and capability on services', () => {
    const url = fleetServicesUrl({ businessDomain: '(none)', capability: 'health' });
    expect(url).toBe('#/fleet/services?businessDomain=%28none%29&capability=health');
    expect(parseHash(url)).toEqual({ view: 'fleet-services', params: { businessDomain: '(none)', capability: 'health' } });
    // The registry path keeps its own param.
    expect(parseHash('#/fleet/services?domain=ghcr.io%2Facme').params).toEqual({ domain: 'ghcr.io/acme' });
  });

  it('round-trips the environment on targets only', () => {
    const url = fleetEntityListUrl('target', { environment: 'prod' });
    expect(url).toBe('#/fleet/targets?environment=prod');
    expect(parseHash(url)).toEqual({ view: 'fleet-entity-list', params: { kind: 'target', environment: 'prod' } });
    expect(fleetEntityListUrl('revision', { environment: 'prod' })).toBe('#/fleet/revisions');
    expect(parseHash('#/fleet/revisions?environment=prod').params).toEqual({ kind: 'revision' });
  });

  it('round-trips the business domain on attention', () => {
    const url = fleetAttentionUrl({ businessDomain: 'commerce', category: 'unowned' });
    expect(url).toBe('#/fleet/attention?category=unowned&businessDomain=commerce');
    expect(parseHash(url).params).toEqual({ category: 'unowned', businessDomain: 'commerce' });
  });
});

describe('business-domain labels', () => {
  it('names the kind and the unowned category', () => {
    expect(kindLabel('domain')).toBe('Domain');
    expect(ATTENTION_CATEGORIES).toContain('unowned');
    expect(attentionCategoryLabel('unowned')).toBe('No declared owner');
  });

  it('says No domain for a service without one', () => {
    expect(businessDomainLabel('payments')).toBe('payments');
    expect(businessDomainLabel('')).toBe('No domain');
    expect(businessDomainLabel(undefined)).toBe('No domain');
  });

  it('reads Unknown, never a healthy tone, without a declared environment', () => {
    expect(targetEnvironmentLabel({ name: 'prod', declared: 'declared' })).toBe('prod');
    expect(targetEnvironmentLabel({ name: '', declared: 'unknown' })).toBe('Unknown');
    expect(targetEnvironmentLabel(undefined)).toBe('Unknown');
    expect(targetEnvironmentTone({ name: 'prod', declared: 'declared', check: { status: 'pass' } })).toBe('ok');
    expect(targetEnvironmentTone({ name: 'prod', declared: 'declared', check: { status: 'fail' } })).toBe('err');
    expect(targetEnvironmentTone({ name: 'prod', declared: 'declared', check: { status: 'unverified' } })).toBe('neutral');
    expect(targetEnvironmentTone({ name: 'prod', declared: 'declared' })).toBe('neutral');
    expect(targetEnvironmentTone({ name: 'prod', declared: 'not-declared' })).toBe('warn');
    expect(targetEnvironmentTone({ name: '', declared: 'unknown' })).toBe('neutral');
    expect(targetEnvironmentTone(undefined)).toBe('neutral');
  });
});

describe('identity context names the registry and the domain apart', () => {
  it('reads registry for the registry path and domain for the business domain', () => {
    expect(identityContext({ label: 'payments', domain: 'ghcr.io/acme' })).toEqual(['registry ghcr.io/acme']);
    expect(identityContext({ label: 'payments', businessDomain: 'commerce', domain: 'ghcr.io/acme' }))
      .toEqual(['domain commerce', 'registry ghcr.io/acme']);
    // A domain row's own name is never repeated as its context.
    expect(identityContext({ kind: 'domain', label: 'commerce', businessDomain: 'commerce' })).toEqual([]);
  });
});

describe('breadcrumbs reach domain pages', () => {
  it('domain: Overview > Domains > commerce', () => {
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'domain', key: 'commerce', label: 'commerce' } });
    expect(t.map((c) => c.label)).toEqual(['Overview', 'Domains', 'commerce']);
    expect(t[1].href).toBe('#/fleet/domains');
  });

  it('a service in a domain goes through its domain', () => {
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'service', key: 'payments', label: 'payments', businessDomain: 'commerce' } });
    expect(t.map((c) => c.label)).toEqual(['Overview', 'Domains', 'commerce', 'payments']);
    expect(t[2].href).toBe('#/fleet/domains/commerce');
  });

  it('a service with no domain goes through Services', () => {
    const t = fleetEntityBreadcrumbs({ entity: { kind: 'service', key: 'payments', label: 'payments' } });
    expect(t.map((c) => c.label)).toEqual(['Overview', 'Services', 'payments']);
  });
});
