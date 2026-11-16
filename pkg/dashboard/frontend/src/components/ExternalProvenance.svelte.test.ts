/**
 * Where an external system was recognized (517-S2): a row per declared, inferred and
 * observed source, the confidence in words, a title on every truncated value, the
 * hidden consumers counted, the rows capped at 20 with "N more" and a labelled block
 * per row that the container lays out.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ExternalProvenance from './ExternalProvenance.svelte';
// @ts-expect-error — a raw import has no declaration file
import source from './ExternalProvenance.svelte?raw';

const inferred = (i: number, over = {}) => ({
  service: `checkout-${i}`, source: 'k8s:prod', kind: 'env', where: `deployment/checkout-${i} env STRIPE_API_BASE`,
  host: 'api.stripe.com:443', confidence: 'likely', entry: 'stripe', catalog: `sha256:${'3e9a'.repeat(16)}`,
  observedAt: '2026-11-16T10:00:00Z', ...over,
});
const data = (over = {}) => ({
  external: {
    key: 'stripe', label: 'Stripe', class: 'vendor-api', consumers: ['checkout'], consumersTotal: 1, sourcesTotal: 1,
    sources: [{ service: 'checkout', index: 0, dependency: 'stripe', kind: 'contract', match: 'identity', provenance: 'declared', required: true, version: '2024-06-20' }],
  },
  consumers: [],
  inferredSources: [inferred(0), inferred(1, { kind: 'secretKeyRef', host: '', confidence: 'possible', where: 'deployment/checkout-1 secretKeyRef STRIPE_SECRET_KEY' })],
  inferredSourcesTotal: 2,
  observedSources: [{ service: 'checkout', source: 'otel', count: 12, lastSeen: '2026-11-16T09:00:00Z' }],
  observedSourcesTotal: 1,
  ...over,
});

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });
function render(d: unknown) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ExternalProvenance, { target, props: { data: d } });
  flushSync();
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}
const rows = (t: Element) => [...t.querySelectorAll('[data-testid="external-source"]')];
const text = (el: Element | null | undefined) => el?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

describe('ExternalProvenance', () => {
  it('lists declared, observed and inferred rows with their kind, where and host', () => {
    const t = render(data());
    expect(rows(t).map((r) => r.getAttribute('data-provenance'))).toEqual(['declared', 'observed', 'inferred', 'inferred']);
    const env = rows(t)[2];
    expect(text(env.querySelector('[data-cell="where"]'))).toContain('Env deployment/checkout-0 env STRIPE_API_BASE');
    expect(text(env.querySelector('[data-cell="host"]'))).toContain('api.stripe.com:443');
    expect(text(rows(t)[0].querySelector('[data-cell="where"]'))).toContain('pacto.yaml dependencies[0] (name: stripe)');
    expect(text(rows(t)[1].querySelector('[data-cell="confidence"]'))).toBe('Confidence Observed, 12 calls');
  });

  it('says the confidence in words and marks inferred rows', () => {
    const t = render(data());
    expect(text(rows(t)[2].querySelector('[data-cell="confidence"]'))).toBe('Confidence Likely');
    expect(text(rows(t)[3].querySelector('[data-cell="confidence"]'))).toBe('Confidence Possible (key name only)');
    expect(rows(t)[2].classList.contains('xp-inferred')).toBe(true);
    expect(rows(t)[2].querySelector('.xp-mark')?.getAttribute('title')).toMatch(/^Inferred from configuration: k8s:prod read it/);
  });

  it('puts a title with the full value on every truncated value', () => {
    const long = `deployment/${'very-long-workload-name-'.repeat(10)} env STRIPE_API_BASE`;
    const t = render(data({ inferredSources: [inferred(0, { where: long })], inferredSourcesTotal: 1 }));
    for (const el of t.querySelectorAll('.xp-trunc')) {
      const holder = el.closest('[title]') ?? el.querySelector('[title]');
      expect(holder?.getAttribute('title')).toBe(el.textContent?.trim());
    }
    expect(t.querySelector(`bdi[title="${long}"]`)).not.toBeNull();
  });

  it('hides the source, catalog entry and read time under More details', () => {
    const t = render(data());
    expect(t.querySelector('[data-cell="details"]')).toBeNull();
    (t.querySelector('[data-testid="external-more-details"]') as HTMLButtonElement).click();
    flushSync();
    expect(text(rows(t)[2].querySelector('[data-cell="details"]'))).toMatch(/^source k8s:prod · catalog entry stripe · catalog 3e9a3e9a3e9a · read /);
  });

  it('filters the inferred rows out and back', () => {
    const t = render(data());
    const box = t.querySelector('[data-testid="external-show-inferred"]') as HTMLInputElement;
    box.click();
    flushSync();
    expect(rows(t).map((r) => r.getAttribute('data-provenance'))).toEqual(['declared', 'observed']);
  });

  it('counts the consumers your permissions hide', () => {
    const d = data();
    (d.external as Record<string, unknown>).hiddenConsumers = 3;
    expect(text(render(d).querySelector('[data-testid="external-hidden-consumers"]'))).toBe('3 hidden consumers: their sources are not listed');
  });

  it('shows 500 sources as 20 rows and 480 more', () => {
    const many = Array.from({ length: 500 }, (_, i) => inferred(i));
    const t = render(data({ external: { ...data().external, sources: [] }, observedSources: [], observedSourcesTotal: 0, inferredSources: many, inferredSourcesTotal: 500 }));
    expect(rows(t).length).toBe(20);
    expect(text(t.querySelector('[data-testid="external-sources-more"]'))).toBe('480 more');
  });

  it('counts the sources the answer does not list', () => {
    const t = render(data({ external: { ...data().external, sources: [] }, observedSources: [], observedSourcesTotal: 0,
      inferredSources: Array.from({ length: 50 }, (_, i) => inferred(i)), inferredSourcesTotal: 57 }));
    (t.querySelector('[data-testid="external-sources-show-all"]') as HTMLButtonElement).click();
    flushSync();
    expect(rows(t).length).toBe(50);
    expect(text(t.querySelector('[data-testid="external-sources-more"]'))).toBe('7 more');
  });

  it('stacks each row into a labelled block that the container lays out', () => {
    const t = render(data());
    expect([...rows(t)[2].querySelectorAll('.xp-k')].map((k) => k.textContent)).toEqual(['Consumer', 'Found in', 'Host', 'Confidence']);
    expect(source).toMatch(/\.xp-list \{[^}]*container-type: inline-size/);
    expect(source).toMatch(/@container \(min-width: 48rem\)/);
    expect(source).not.toMatch(/@media \([^)]*width/);
  });
});
