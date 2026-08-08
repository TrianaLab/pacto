/**
 * Render tests for the Phase-2 product components. These assert the behaviors that
 * matter for the product journey: entity links use canonical/backend hrefs (never
 * invented), empty states are honest about knowledge, source health is navigable and
 * least-healthy-first, and summary counts navigate to real filtered views.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import EntityLink from './EntityLink.svelte';
// @ts-expect-error
import EntityIdentity from './EntityIdentity.svelte';
// @ts-expect-error
import ProductEmptyState from './ProductEmptyState.svelte';
// @ts-expect-error
import SourceHealth from './SourceHealth.svelte';
// @ts-expect-error
import OperationalSummary from './OperationalSummary.svelte';
// @ts-expect-error
import PreviewSection from './PreviewSection.svelte';
import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

describe('EntityLink', () => {
  it('adopts the authoritative backend href verbatim', () => {
    comp = mount(EntityLink, { target, props: { ref: { kind: 'target', key: 'prod/k8s/app', label: 'app', href: '/fleet/targets/prod%2Fk8s%2Fapp' } } });
    const a = target.querySelector('a.entity-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('#/fleet/targets/prod%2Fk8s%2Fapp');
  });

  it('falls back to the centralized (kind,key) builder when no href is present', () => {
    comp = mount(EntityLink, { target, props: { ref: { kind: 'service', key: 'domain-a/payments', label: 'payments' } } });
    const a = target.querySelector('a.entity-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('#/fleet/services/domain-a%2Fpayments');
  });
});

describe('EntityIdentity disambiguation (requirement F item 7)', () => {
  it('shows domain so same-named services in different domains are distinguishable', () => {
    comp = mount(EntityIdentity, { target, props: { ref: { kind: 'service', key: 'domain-a/payments', label: 'payments', domain: 'domain-a' } } });
    expect(target.textContent).toContain('payments');
    expect(target.textContent).toContain('domain domain-a');
    expect(target.textContent).toContain('Service');
  });
});

describe('ProductEmptyState — honest knowledge states (requirement H)', () => {
  it('an empty result under incomplete knowledge is NEVER an all-clear', () => {
    const partial = snapshotKnowledge({ sources: [{ status: 'partial' }] });
    const state = decideViewState({ loading: false, itemCount: 0, knowledge: partial });
    comp = mount(ProductEmptyState, { target, props: { state, noun: 'attention items' } });
    const text = target.textContent || '';
    expect(text).toContain('No attention items known');
    expect(text).toContain('Partial knowledge');
    expect(text).not.toMatch(/all clear/i);
    expect(text).not.toMatch(/everything is healthy/i);
  });

  it('a genuinely empty fleet (complete knowledge) reads as empty, not degraded', () => {
    const complete = snapshotKnowledge({ completeness: 'complete', sources: [{ status: 'available' }] });
    const state = decideViewState({ loading: false, itemCount: 0, knowledge: complete });
    comp = mount(ProductEmptyState, { target, props: { state, noun: 'services' } });
    expect(target.textContent).toContain('No services yet');
    expect(target.textContent).not.toContain('incomplete');
  });

  it('a filtered-empty result offers to clear filters', () => {
    let cleared = false;
    const state = decideViewState({ loading: false, itemCount: 0, filtered: true, knowledge: snapshotKnowledge(null) });
    comp = mount(ProductEmptyState, { target, props: { state, noun: 'results', onClearFilters: () => { cleared = true; } } });
    expect(target.textContent).toContain('No matching results');
    (target.querySelector('.ps-btn') as HTMLButtonElement).click();
    expect(cleared).toBe(true);
  });

  it('a backend error is distinct from empty and offers retry', () => {
    const state = decideViewState({ loading: false, itemCount: 0, error: new Error('down') });
    comp = mount(ProductEmptyState, { target, props: { state, onRetry: () => {} } });
    expect(target.textContent).toContain('Can’t reach the Pacto backend');
  });

  it('scenario 14: an unknown entity (404) renders a real not-found state', async () => {
    const { ApiError } = await import('../lib/api.ts');
    const state = decideViewState({ loading: false, itemCount: 0, error: new ApiError(404, 'no revision with this key') });
    comp = mount(ProductEmptyState, { target, props: { state, noun: 'revision' } });
    expect(state.kind).toBe('not-found');
    expect(target.textContent).toContain('Not found');
    expect(target.textContent).toContain('no revision with this key');
  });

  it('scenario 15: a schema/contract incompatibility renders an explicit error state', async () => {
    const { SchemaCompatibilityError } = await import('../lib/api.ts');
    const state = decideViewState({ loading: false, itemCount: 0, error: new SchemaCompatibilityError('pacto.dev/fleet-product/v2') });
    comp = mount(ProductEmptyState, { target, props: { state } });
    expect(state.kind).toBe('schema-error');
    expect(target.textContent).toContain('Dashboard is out of date');
  });
});

describe('SourceHealth', () => {
  it('lists least-healthy first and links each source to its detail', () => {
    comp = mount(SourceHealth, {
      target,
      props: { sources: [{ id: 'oci', status: 'available' }, { id: 'k8s', status: 'unavailable' }, { id: 'local', status: 'stale' }] },
    });
    const links = Array.from(target.querySelectorAll('a.sh-chip')) as HTMLAnchorElement[];
    expect(links[0].textContent).toContain('k8s');       // unavailable first
    expect(links[0].getAttribute('href')).toBe('#/fleet/sources/k8s');
    expect(links[1].textContent).toContain('local');     // then stale
    expect(links[2].textContent).toContain('oci');       // available last
  });
});

describe('OperationalSummary', () => {
  it('renders attention entry-point tiles with their backend hrefs and a revision-match breakdown', () => {
    comp = mount(OperationalSummary, {
      target,
      props: {
        summary: { servicesNeedingAttention: 2, exactTargetLinks: 5, inferredTargetLinks: 1, ambiguousTargetLinks: 1, unresolvedTargetLinks: 0, observedOnlyRelationships: 3 },
        entryPoints: [
          { label: 'Non-compliant deployments', count: 2, view: 'attention', href: '/fleet/attention?category=non-compliant' },
          { label: 'Deployments with stale evidence', count: 4, view: 'attention', href: '/fleet/attention?category=stale' },
        ],
        attentionTotal: 6,
      },
    });
    const nonCompliant = Array.from(target.querySelectorAll('a.tile')).find((t) => t.textContent?.includes('Non-compliant')) as HTMLAnchorElement;
    expect(nonCompliant.getAttribute('href')).toBe('#/fleet/attention?category=non-compliant');
    // Revision-match breakdown surfaces all four certainty buckets.
    const rm = target.querySelector('.rev-match')?.textContent || '';
    expect(rm).toContain('Exact');
    expect(rm).toContain('Ambiguous');
    // The lead tile links to the full attention list.
    const lead = target.querySelector('a.tile-lead') as HTMLAnchorElement;
    expect(lead.getAttribute('href')).toBe('#/fleet/attention');
  });
});

describe('ProductEmptyState — filtered-empty never hides incompleteness (requirement D)', () => {
  it('a filter matching nothing UNDER incomplete knowledge shows BOTH facts', () => {
    const partial = snapshotKnowledge({ sources: [{ status: 'partial' }] });
    const state = decideViewState({ loading: false, itemCount: 0, filtered: true, knowledge: partial });
    comp = mount(ProductEmptyState, { target, props: { state, noun: 'services' } });
    const text = target.textContent || '';
    expect(text).toContain('No matching services');   // the filter matched nothing
    expect(text).toContain('Partial knowledge');       // AND knowledge is incomplete
  });

  it('a filter matching nothing under COMPLETE knowledge shows no incompleteness caveat', () => {
    const complete = snapshotKnowledge({ completeness: 'complete', sources: [{ status: 'available' }] });
    const state = decideViewState({ loading: false, itemCount: 0, filtered: true, knowledge: complete });
    comp = mount(ProductEmptyState, { target, props: { state, noun: 'services' } });
    const text = target.textContent || '';
    expect(text).toContain('No matching services');
    expect(text).not.toMatch(/knowledge/i);
    expect(text).not.toMatch(/incomplete/i);
  });
});

describe('PreviewSection — honest known vs unknown totals (requirement B)', () => {
  const count = (t: HTMLElement) => t.querySelector('[data-testid="preview-count"]')?.textContent?.trim() ?? '';
  const more = (t: HTMLElement) => t.querySelector('[data-testid="preview-more"]')?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

  it('known total keeps the count-of-total behavior', () => {
    comp = mount(PreviewSection, { target, props: { title: 'Findings', total: 37, count: 20, truncated: true } });
    expect(count(target)).toBe('20 of 37');
    expect(more(target)).toContain('Showing 20 of 37.');
  });

  it('known total, not truncated: count of total, no continuation line', () => {
    comp = mount(PreviewSection, { target, props: { title: 'Revisions', total: 5, count: 5, truncated: false } });
    expect(count(target)).toBe('5 of 5');
    expect(target.querySelector('[data-testid="preview-more"]')).toBeNull();
  });

  it('unknown total + truncated NEVER says X of X (service relationships case)', () => {
    // RelationshipsPreview from an already-truncated neighborhood: count=200, total absent.
    comp = mount(PreviewSection, { target, props: { title: 'Observed traffic and differences', total: null, count: 200, truncated: true } });
    const text = target.textContent || '';
    expect(text).not.toContain('200 of 200');
    expect(text).not.toMatch(/\bof\b/); // no synthesized "of <total>" anywhere
    expect(count(target)).toBe('200');
    expect(more(target)).toBe('Showing 200. More exist; total unknown.');
  });

  it('unknown total + not truncated: just the count, no continuation line', () => {
    comp = mount(PreviewSection, { target, props: { title: 'Observed runtime', total: null, count: 20, truncated: false } });
    expect(count(target)).toBe('20');
    expect((target.textContent || '')).not.toMatch(/\bof\b/);
    expect(target.querySelector('[data-testid="preview-more"]')).toBeNull();
  });
});
