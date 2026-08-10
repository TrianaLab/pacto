/**
 * Component tests for FleetOverview.svelte — the operational landing page.
 * Covers acceptance scenarios 1-5: the overview loads from the product contract,
 * partial knowledge with zero attention NEVER shows "All clear", a genuinely
 * healthy complete state MAY, attention items and category tiles navigate to exact
 * destinations, and a degraded source is visible and navigable. `api` is mocked so
 * no network is hit and only /api/fleet/overview is consumed (never the snapshot).
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { overviewFn } = vi.hoisted(() => ({ overviewFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetOverview: (...a: unknown[]) => overviewFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetOverview from './FleetOverview.svelte';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
function baseOverview(partial = false): any {
  return {
    meta: {
      schemaVersion: 'pacto.dev/fleet-product/v1',
      snapshotId: 'sha256:abc',
      asOf: '2026-07-29T10:00:00Z',
      completeness: partial ? 'partial' : 'complete',
      sources: partial
        ? [{ id: 'oci', kind: 'oci', status: 'available' }, { id: 'k8s', kind: 'k8s', status: 'unavailable' }]
        : [{ id: 'oci', kind: 'oci', status: 'available' }],
    },
    summary: {
      services: 3, servicesNeedingAttention: 0, revisions: 6, targets: 5,
      exactTargetLinks: 4, inferredTargetLinks: 1,
      ambiguousTargetLinks: 0, unresolvedTargetLinks: 0,
      compliantTargets: 5, nonCompliantTargets: 0, unknownTargets: 0, invalidTargets: 0, otherComplianceTargets: 0,
      staleTargets: 0, unresolvedRelationships: 0, observedOnlyRelationships: 0, recentEvidence: 0,
      // Backend tallies over the COMPLETE populations: ownership over all 3 services,
      // readiness over all 6 contract revisions.
      ownership: { consistent: 2, conflicting: 0, unowned: 1 },
      readiness: { passing: 4, belowThreshold: 1, expired: 0, notDeclared: 1 },
    },
    attention: { total: 0, count: 0, truncated: false, items: [] },
    recentEvidence: { total: 0, count: 0, truncated: false, items: [] },
    entryPoints: [
      { label: 'Operational targets not compliant', count: 0, view: 'attention', category: 'non-compliant', href: '/fleet/attention?category=non-compliant' },
    ],
  };
}

function mountView() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetOverview, { target, props: { refreshTick: 0 } });
  return { target, component };
}

describe('FleetOverview — operational landing (scenarios 1-5)', () => {
  beforeEach(() => overviewFn.mockReset());

  it('scenario 1: loads the product overview and renders the summary', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => {
      expect(target.textContent).toContain('Operational overview');
      expect(target.textContent).toContain('Revision-match certainty');
    });
    expect(overviewFn).toHaveBeenCalledTimes(1); // consumes /api/fleet/overview, not the snapshot
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 2: partial source + zero attention NEVER shows "All clear"', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    // Wait for the loaded content (the summary), not the always-present header.
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).not.toMatch(/all clear/i);
    expect(text).toContain('Source unavailable'); // the incompleteness is shown honestly
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 3: complete + zero attention MAY show an all-clear state', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent).toMatch(/all clear/i));
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 4: an attention item navigates to its exact entity; a category tile to its filter', async () => {
    const ov = baseOverview(false);
    ov.summary.servicesNeedingAttention = 1;
    ov.attention = {
      total: 1, count: 1, truncated: false,
      items: [{ entity: { kind: 'target', key: 'prod/k8s/app', label: 'app', href: '/fleet/targets/prod%2Fk8s%2Fapp', status: 'NonCompliant' }, severity: 'error', category: 'non-compliant', summary: 'contract violation', label: 'app' }],
    };
    ov.entryPoints = [{ label: 'Operational targets not compliant', count: 1, view: 'attention', category: 'non-compliant', href: '/fleet/attention?category=non-compliant' }];
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.attn-item')).toBeTruthy());
    // attention item -> exact entity href
    const entityLink = target.querySelector('.attn-item a.entity-link') as HTMLAnchorElement;
    expect(entityLink.getAttribute('href')).toBe('#/fleet/targets/prod%2Fk8s%2Fapp');
    // category tile -> exact filtered attention view
    const tile = Array.from(target.querySelectorAll('a.tile')).find((t) => t.textContent?.includes('not compliant')) as HTMLAnchorElement;
    expect(tile.getAttribute('href')).toBe('#/fleet/attention?category=non-compliant');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 5: a degraded source is visible and navigable to its detail', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.source-health')).toBeTruthy());
    const k8s = Array.from(target.querySelectorAll('a.sh-chip')).find((c) => c.textContent?.includes('k8s')) as HTMLAnchorElement;
    expect(k8s).toBeTruthy();
    expect(k8s.getAttribute('href')).toBe('#/fleet/sources/k8s');
    expect(k8s.textContent).toContain('Unavailable');
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetOverview — A1: an empty fleet is never "All clear"', () => {
  beforeEach(() => overviewFn.mockReset());

  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
  function emptyOverview(partial: boolean): any {
    const ov = baseOverview(partial);
    ov.summary.services = 0;
    ov.summary.exactTargetLinks = 0; ov.summary.inferredTargetLinks = 0;
    ov.summary.ambiguousTargetLinks = 0; ov.summary.unresolvedTargetLinks = 0;
    ov.attention = { total: 0, count: 0, truncated: false, items: [] };
    return ov;
  }

  it('case 1: complete knowledge + zero services renders a genuine empty-fleet state, not all-clear', async () => {
    overviewFn.mockResolvedValue(emptyOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).not.toMatch(/all clear/i);
    expect(text).not.toMatch(/every operational target is compliant/i);
    expect(target.querySelector('.empty-fleet')).toBeTruthy();
    expect(text).toMatch(/no services tracked/i);
    unmount(component); document.body.removeChild(target);
  });

  it('case 2: incomplete knowledge + zero services never claims health', async () => {
    overviewFn.mockResolvedValue(emptyOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).not.toMatch(/all clear/i);
    expect(text).not.toMatch(/every operational target is compliant/i);
    expect(target.querySelector('.empty-fleet')).toBeFalsy(); // incomplete: not a confirmed-empty claim either
    expect(target.querySelector('.knowledge-banner')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('case 3: a populated healthy fleet with zero attention MAY show all-clear', async () => {
    overviewFn.mockResolvedValue(baseOverview(false)); // services: 3, targets > 0, complete
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.all-clear')).toBeTruthy());
    expect(target.textContent).toMatch(/every operational target is compliant/i);
    unmount(component); document.body.removeChild(target);
  });

  it('case 4: a populated but incomplete fleet with zero attention does NOT show all-clear', async () => {
    overviewFn.mockResolvedValue(baseOverview(true)); // services: 3, but a source is unavailable
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    expect(target.querySelector('.all-clear')).toBeFalsy();
    expect(target.querySelector('.knowledge-banner')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('requirement D: an `empty`-completeness fleet is honestly empty, NOT "sources degraded"', async () => {
    // Every source healthy, no record: completeness `empty`. This is a confidently
    // empty fleet, not degraded knowledge and not an all-clear health assessment.
    const ov = baseOverview(false);
    ov.meta.completeness = 'empty';
    ov.summary.services = 0;
    ov.summary.exactTargetLinks = 0; ov.summary.inferredTargetLinks = 0;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    // The honest empty-fleet message shows; the degraded-source banner and all-clear do NOT.
    expect(target.querySelector('.empty-fleet')).toBeTruthy();
    expect(target.querySelector('.knowledge-banner')).toBeFalsy();
    expect(target.querySelector('.all-clear')).toBeFalsy();
    expect(text).not.toMatch(/sources are degraded/i);
    expect(text).toMatch(/no services tracked yet/i);
    unmount(component); document.body.removeChild(target);
  });
});

/**
 * The three bands. Each one answers a different question, and each fact inside a band
 * is drawn over a COMPLETE population the backend tallied -- never over the truncated
 * attention or evidence previews further down the same page.
 */
describe('FleetOverview — every band draws a complete population', () => {
  beforeEach(() => overviewFn.mockReset());

  const sectionText = (target: HTMLElement, id: string) =>
    (target.querySelector(`section[aria-labelledby="${id}"]`) as HTMLElement | null)?.textContent || '';
  const linkIn = (target: HTMLElement, id: string, text: string) =>
    Array.from(target.querySelectorAll(`section[aria-labelledby="${id}"] a`))
      .find((a) => a.textContent?.includes(text)) as HTMLAnchorElement | undefined;

  it('band 2 draws posture over the backend target population and links each bucket to its triage', async () => {
    const ov = baseOverview(false);
    ov.summary.exactTargetLinks = 3; ov.summary.ambiguousTargetLinks = 1; // still 5 targets
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('section[aria-labelledby="ov-posture"]')).toBeTruthy());
    const posture = sectionText(target, 'ov-posture');
    // Compliance, revision-match certainty and evidence freshness are three separate
    // questions, each printed as exact text beside its bar (nothing is colour-only).
    expect(posture).toContain('Compliant');
    expect(posture).toContain('Exact');
    expect(posture).toContain('5 operational targets');
    expect(posture).toContain('exactly which revision is running on 3 of 5');
    // A proportion is a way in, not a picture.
    expect(linkIn(target, 'ov-posture', 'Ambiguous')?.getAttribute('href')).toBe('#/fleet/attention?category=unresolved');
    unmount(component); document.body.removeChild(target);
  });

  // The denominator is the backend's Targets count, never the sum of the buckets it was
  // handed: if a bucket is missing, the gap shows as an explicit unclassified slice
  // rather than silently rescaling the proportion to whatever added up.
  it('band 2 shows the gap when the compliance buckets do not account for the whole population', async () => {
    const ov = baseOverview(false);
    ov.summary.compliantTargets = 1; // 5 targets, 1 accounted for
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('section[aria-labelledby="ov-posture"]')).toBeTruthy());
    const posture = sectionText(target, 'ov-posture');
    expect(posture).toContain('Unclassified');
    expect(posture).toContain('4');
    unmount(component); document.body.removeChild(target);
  });

  it('band 3 partitions every service by declared ownership and drills into that exact filter', async () => {
    const ov = baseOverview(false);
    ov.summary.ownership = { consistent: 1, conflicting: 1, unowned: 1 };
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('section[aria-labelledby="ov-org"]')).toBeTruthy());
    const org = sectionText(target, 'ov-org');
    expect(org).toContain('Declared ownership');
    expect(org).toContain('All 3 services in the snapshot.');
    // Two teams claiming one service is its own state, never folded into "no owner".
    expect(org).toContain('Revisions name different owners');
    expect(linkIn(target, 'ov-org', 'Revisions name different owners')?.getAttribute('href'))
      .toBe('#/fleet/services?ownership=conflicting');
    expect(linkIn(target, 'ov-org', 'No declared owner')?.getAttribute('href'))
      .toBe('#/fleet/services?ownership=unowned');
    unmount(component); document.body.removeChild(target);
  });

  // Owners is a dimension, not a fifth primary destination, so the nav does not carry
  // it. A reader shown a fleet-wide ownership gap has to be able to get from that fact
  // to whose gap it is without knowing the URL.
  it('band 3 is a door to Owners, which the primary nav deliberately does not carry', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('section[aria-labelledby="ov-org"]')).toBeTruthy());
    expect(linkIn(target, 'ov-org', 'Browse owners')?.getAttribute('href')).toBe('#/fleet/owners');
    unmount(component); document.body.removeChild(target);
  });

  // Readiness is declared BY a contract revision, so the unit is always the revision --
  // never the service, the fleet, the runtime or the operational target.
  it('band 3 partitions every contract revision by its own declared assessment', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('section[aria-labelledby="ov-org"]')).toBeTruthy());
    const org = sectionText(target, 'ov-org');
    expect(org).toContain('Contract revision readiness');
    expect(org).toContain('All 6 contract revisions in the snapshot.');
    expect(org).toContain('Below its own threshold');
    expect(linkIn(target, 'ov-org', 'Below its own threshold')?.getAttribute('href'))
      .toBe('#/fleet/revisions?readiness=below-threshold');
    expect(linkIn(target, 'ov-org', 'Not assessed')?.getAttribute('href'))
      .toBe('#/fleet/revisions?readiness=not-declared');
    // A readiness-passing revision can still run on a target observed to violate its
    // contract, so the page must never conflate the two -- or rename the unit.
    expect(org).not.toMatch(/service readiness|fleet readiness|fleet health|runtime readiness|target readiness/i);
    unmount(component); document.body.removeChild(target);
  });

  it('an empty fleet says the population is empty instead of drawing a bar over nothing', async () => {
    const ov = baseOverview(false);
    ov.summary.services = 0; ov.summary.revisions = 0;
    ov.summary.ownership = {}; ov.summary.readiness = {};
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('section[aria-labelledby="ov-org"]')).toBeTruthy());
    const org = sectionText(target, 'ov-org');
    expect(org).toContain('No services are tracked yet');
    expect(org).toContain('No contract revisions are tracked yet');
    unmount(component); document.body.removeChild(target);
  });
});
