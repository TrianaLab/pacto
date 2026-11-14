/**
 * The resources a service consumes: one card per use with its provider, owner,
 * availability, the request it declares, what providers report per environment (with
 * the provenance as visible text) and the usage state with a Mira pointer.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ResourceList from './ResourceList.svelte';

const ref = (kind: string, key: string, label = key) => ({ kind, key, label, href: `/fleet/${kind}s/${encodeURIComponent(key)}` });
const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();
const consumer = ref('service', 'catalog-service');
const pg = ref('service', 'postgresql');
const notReported = [{ state: 'not-reported', provenance: 'observed', reason: 'no provider reports usage or cost for this resource to Pacto' }];

// catalog-service: one row via the configuration reference (with the request) and one via the dependency.
const catalog = [
  {
    offering: 'postgresql', consumer, provider: pg, via: 'configuration_reference',
    providerOwner: { owner: 'infra-data', ref: ref('owner', 'team:infra-data', 'infra-data'), declared: true },
    providerAvailability: { state: 'unknown', byEnvironment: [], total: 0, asOf: '2026-11-04T10:00:00Z' },
    requested: [{ entry: 'database', configuration: 'provisioning', revision: ref('revision', 'postgresql@sha256:1', 'postgresql 16.1.0'), provenance: 'declared',
      values: { total: 2, count: 2, scanned: 2, truncated: false, items: [{ key: 'size', value: 'medium' }, { key: 'storageGi', value: '50' }] } }],
    provisioning: [
      { environment: 'production', phase: 'Ready', provenance: 'observed', verified: true, producer: 'platform-db', source: 'evidence', provider: 'crossplane', externalRef: 'databases/catalog-production', asOf: '2026-11-04T09:00:00Z' },
      { environment: 'staging', phase: 'Unknown', provenance: 'observed', verified: false, source: 'target-state', stale: true, reportedPhase: 'Ready' },
    ],
    consumption: notReported,
  },
  { offering: 'postgresql', consumer, provider: pg, via: 'dependency', compatibility: '^16.0.0', rangeStatus: 'satisfied', provisioning: [], consumption: notReported },
];

function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ResourceList, { target, props: { service: 'catalog-service', ...props } });
  flushSync();
  return { target, done: () => { unmount(c); target.remove(); } };
}

describe('ResourceList', () => {
  it('links the provider, its owner and the offering', () => {
    const { target, done } = render({ uses: catalog });
    const card = target.querySelector('[data-testid="service-resource"]')!;
    const hrefs = Array.from(card.querySelectorAll('a[href]')).map((a) => a.getAttribute('href'));
    expect(hrefs).toContain('#/fleet/services/postgresql');
    expect(hrefs).toContain('#/fleet/offerings/postgresql');
    expect(hrefs).toContain('#/fleet/owners/team%3Ainfra-data');
    expect(text(card)).toContain('provider unknown');
    done();
  });

  it('says the owner is unknown and the resource has no offering when neither is known', () => {
    const { target, done } = render({ uses: [{ offering: '', consumer, provider: ref('service', 'email-provider'), via: 'dependency', provisioning: [] }] });
    const card = target.querySelector('[data-testid="service-resource"]')!;
    expect(text(card)).toContain('Owner unknown');
    expect(text(card)).toContain('no offering');
    expect(card.querySelector('a[href^="#/fleet/offerings/"]')).toBeNull();
    done();
  });

  it('shows the request once, on the configuration-reference row only', () => {
    const { target, done } = render({ uses: catalog });
    const blocks = target.querySelectorAll('[data-testid="resource-requested"]');
    expect(blocks.length).toBe(1);
    expect(text(blocks[0])).toContain('Requested (declared)');
    expect(text(blocks[0])).toContain('postgresql 16.1.0');
    expect(text(blocks[0])).toContain('database, configuration provisioning');
    expect(Array.from(blocks[0].querySelectorAll('dt')).map(text)).toEqual(['size', 'storageGi']);
    expect(Array.from(blocks[0].querySelectorAll('dd')).map(text)).toEqual(['medium', '50']);
    done();
  });

  it('states provenance as visible text for signed and unsigned reports', () => {
    const { target, done } = render({ uses: catalog });
    const rows = Array.from(target.querySelectorAll('[data-testid="resource-status"]')).map(text);
    expect(rows[0]).toContain('production');
    expect(rows[0]).toContain('reported by platform-db through evidence');
    expect(rows[0]).toContain('adapter crossplane');
    expect(rows[0]).toContain('databases/catalog-production');
    expect(rows[1]).toContain('Unknown: last reported Ready, stale');
    expect(rows[1]).toContain('unsigned');
    done();
  });

  it('opens the request rail from a badge', () => {
    const { target, done } = render({ uses: catalog });
    expect(target.querySelector('[data-testid="offering-use-figure"]')).toBeNull();
    (target.querySelector('[data-testid="provisioning-badge"]') as HTMLButtonElement).click();
    flushSync();
    expect(target.querySelector('[data-testid="offering-use-figure"]')).toBeTruthy();
    done();
  });

  it('says usage is not reported and links Mira only when given a link', () => {
    const without = render({ uses: catalog });
    expect(text(without.target.querySelector('[data-testid="resource-usage"]'))).toContain('Not reported: no provider reports usage or cost');
    expect(without.target.querySelector('[data-testid="resource-mira-link"]')).toBeNull();
    without.done();
    const withMira = render({ uses: catalog, miraHref: 'https://mira.example/s?service=catalog-service' });
    const a = withMira.target.querySelector('[data-testid="resource-mira-link"]') as HTMLAnchorElement;
    expect(a.textContent).toBe('Open catalog-service in Mira');
    expect(a.getAttribute('rel')).toBe('noopener noreferrer');
    withMira.done();
  });
});
