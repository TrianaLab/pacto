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

  it('abbreviates a content digest instead of overflowing the row, keeping the exact value', () => {
    const digest = `sha256:${'a1b2c3d4'.repeat(8)}`;
    comp = mount(EntityIdentity, {
      target,
      props: { ref: { kind: 'revision', key: `svc@${digest}`, label: 'svc 1.0.0', secondary: digest } },
    });
    const bit = Array.from(target.querySelectorAll('.ei-context span')).at(-1) as HTMLElement;
    expect(bit.textContent).toBe('a1b2c3d4a1b2…');
    expect(bit.getAttribute('title')).toBe(digest); // precision preserved, not discarded
  });

  it('renders a data source status as health, matching its own detail page', () => {
    comp = mount(EntityIdentity, { target, props: { ref: { kind: 'source', key: 'edge-cluster', label: 'edge-cluster', status: 'unavailable' } } });
    const badge = target.querySelector('.tag') as HTMLElement;
    expect(badge.textContent).toBe('Unavailable');       // not the raw lowercase enum
    expect(badge.className).toContain('tone-err');       // not neutral grey
    expect(target.querySelector('.status-badge')).toBeNull();
  });

  // Every list row carried its context bits unconditionally, so the commonest rows in
  // the product read the same word twice in 40 pixels and wrapped onto a second line
  // for it: "payments-service 1.2.0 · payments-service" and
  // "prod/k8s/orders-service · orders-service · prod".
  it('drops a context bit the label already spells out', () => {
    comp = mount(EntityIdentity, {
      target,
      props: { ref: { kind: 'revision', key: 'payments-service@sha256:ab', label: 'payments-service 1.2.0', parentService: 'payments-service' } },
    });
    expect(target.querySelector('.ei-context')).toBeNull();
    unmount(comp); comp = null;
    comp = mount(EntityIdentity, {
      target,
      props: { ref: { kind: 'target', key: 'prod/k8s/orders-service', label: 'prod/k8s/orders-service', parentService: 'orders-service', scope: 'prod' } },
    });
    expect(target.querySelector('.ei-context')).toBeNull();
  });

  it('keeps a context bit the label only partly resembles', () => {
    // "orders" is not a whole segment of "prod/k8s/orders-service", and a target whose
    // own name differs from its service still has to say which service it belongs to.
    comp = mount(EntityIdentity, {
      target,
      props: { ref: { kind: 'target', key: 'prod/k8s/gw', label: 'prod/k8s/gw', parentService: 'api-gateway', scope: 'prod' } },
    });
    expect(target.querySelector('.ei-context')?.textContent).toContain('api-gateway');
  });

  it('leaves short human context alone (no tooltip, no ellipsis)', () => {
    comp = mount(EntityIdentity, { target, props: { ref: { kind: 'target', key: 'prod/k8s/app', label: 'app', scope: 'prod' } } });
    const bit = target.querySelector('.ei-context span') as HTMLElement;
    expect(bit.textContent).toBe('prod');
    expect(bit.getAttribute('title')).toBeNull();
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
  const summaryProps = (over = {}) => ({
    summary: {
      servicesNeedingAttention: 2, services: 4, revisions: 9, targets: 7,
      exactTargetLinks: 5, inferredTargetLinks: 1, ambiguousTargetLinks: 1, unresolvedTargetLinks: 0,
      compliantTargets: 4, nonCompliantTargets: 2, unknownTargets: 1, invalidTargets: 0, otherComplianceTargets: 0,
      observedOnlyRelationships: 3, ...over,
    },
    entryPoints: [
      // The backend's first attention entry point is the UNCATEGORISED one: the same
      // "all attention" count the lead tile already shows.
      { label: 'Services needing attention', count: 2, view: 'attention', severity: 'warning', href: '/fleet/attention' },
      { label: 'Operational targets not compliant', count: 2, view: 'attention', category: 'non-compliant', severity: 'error', href: '/fleet/attention?category=non-compliant' },
      { label: 'Operational targets with stale evidence', count: 4, view: 'attention', category: 'stale', severity: 'warning', href: '/fleet/attention?category=stale' },
      { label: 'Undeclared runtime dependencies observed', count: 3, view: 'services', severity: 'info', href: '/fleet/services' },
      // View "overview" means "this very page" -- its href is /fleet.
      { label: 'Incomplete sources', count: 1, view: 'overview', severity: 'warning', href: '/fleet' },
    ],
    attentionTotal: 6,
  });

  // Every entry prints the backend's label verbatim, and only entry points that lead
  // somewhere else are rendered at all. The lead tile and the uncategorised entry point
  // are the same measurement (two wordings, one number); the overview entry point links
  // back to the page it is on, where the source health strip already shows what it counts.
  //
  // Eight identical tiles made the reader redo the ranking the backend had already done,
  // so the backend's own grade now decides the treatment: errors share the grid with the
  // lead tile, everything milder becomes one compact line.
  it('leads with the errors and compacts the milder grades, worded as the backend worded them', () => {
    comp = mount(OperationalSummary, { target, props: summaryProps() });
    expect(Array.from(target.querySelectorAll('.tile-grid .tile-label')).map((n) => n.textContent)).toEqual([
      'Services need attention',
      'Operational targets not compliant',
    ]);
    // Demoting the treatment must not delete the number: the milder entries keep their
    // exact count and stay one click away.
    const sec = Array.from(target.querySelectorAll('.op-secondary a.op-sec'));
    expect(sec.map((n) => n.querySelector('.op-sec-label')?.textContent)).toEqual([
      'Operational targets with stale evidence',
      'Undeclared runtime dependencies observed',
    ]);
    expect(sec.map((n) => n.querySelector('.op-sec-count')?.textContent)).toEqual(['4', '3']);
  });

  it('sends every entry to the backend href it arrived with', () => {
    comp = mount(OperationalSummary, { target, props: summaryProps() });
    const nonCompliant = Array.from(target.querySelectorAll('a.tile')).find((t) => t.textContent?.includes('not compliant')) as HTMLAnchorElement;
    expect(nonCompliant.getAttribute('href')).toBe('#/fleet/attention?category=non-compliant');
    const stale = Array.from(target.querySelectorAll('a.op-sec')).find((a) => a.textContent?.includes('stale evidence')) as HTMLAnchorElement;
    expect(stale.getAttribute('href')).toBe('#/fleet/attention?category=stale');
    // The lead tile links to the full attention list.
    const lead = target.querySelector('a.tile-lead') as HTMLAnchorElement;
    expect(lead.getAttribute('href')).toBe('#/fleet/attention');
  });

  // Every non-zero tile used to be amber, so the overview graded a confirmed drift and
  // an undeclared runtime call the same, and the attention list directly below it then
  // badged them Error and Info. Each entry now carries the backend's grade for the
  // category, which is the grade its own items carry.
  it('grades an entry the same way the items behind it are graded', () => {
    comp = mount(OperationalSummary, { target, props: summaryProps() });
    const toneOf = (text: string) => (Array.from(target.querySelectorAll('a.tile, a.op-sec'))
      .find((t) => t.textContent?.includes(text)) as HTMLElement).className;
    expect(toneOf('not compliant')).toContain('tone-err');
    expect(toneOf('stale evidence')).toContain('tone-warn');
    expect(toneOf('Undeclared runtime dependencies')).toContain('tone-info');
    expect(toneOf('Services need attention')).toContain('tone-warn');
  });

  it('reads a clean count as clean whatever the category grade', () => {
    const props = summaryProps();
    props.entryPoints = props.entryPoints.map((ep) => ({ ...ep, count: 0 }));
    comp = mount(OperationalSummary, { target, props });
    const entry = Array.from(target.querySelectorAll('a.tile, a.op-sec')).find((t) => t.textContent?.includes('Undeclared')) as HTMLElement;
    expect(entry.className).toContain('tone-ok');
  });

  // A grade a newer engine spells differently must not quietly become a footnote: only
  // the grades this build can read AS milder than an error are allowed to be compacted.
  it('promotes an entry point whose grade this build does not recognise', () => {
    const props = summaryProps();
    props.entryPoints = [...props.entryPoints, { label: 'Contracts revoked upstream', count: 1, view: 'services', severity: 'catastrophic', href: '/fleet/services' }];
    comp = mount(OperationalSummary, { target, props });
    expect(Array.from(target.querySelectorAll('.tile-grid .tile-label')).map((n) => n.textContent))
      .toContain('Contracts revoked upstream');
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
