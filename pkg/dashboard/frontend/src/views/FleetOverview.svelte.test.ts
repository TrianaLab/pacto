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

const { overviewFn, entitiesFn, statsFn, mapFn } = vi.hoisted(() => ({ overviewFn: vi.fn(), entitiesFn: vi.fn(), statsFn: vi.fn(), mapFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  // The trust figure's aside asks the service tally separately, the figures their trend
  // and the map its answer; none answers unless a test says so.
  const never = () => new Promise(() => {});
  return { ...actual, api: {
    fleetOverview: (...a: unknown[]) => overviewFn(...a), fleetEntities: (...a: unknown[]) => entitiesFn(...a) ?? never(),
    stats: (...a: unknown[]) => statsFn(...a) ?? never(), fleetMap: (...a: unknown[]) => mapFn(...a) ?? never(),
    fleetMapNodes: never, fleetMapFocus: never, fleetAttention: never,
  } };
});
import { recordingContext } from '../lib/fleetMap/testkit';
beforeEach(() => {
  statsFn.mockReset(); mapFn.mockReset();
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => recordingContext() as never);
});

import { ApiError } from '../lib/api.ts';
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
        ? [{ id: 'oci', kind: 'oci', status: 'available' }, { id: 'k8s', kind: 'kubernetes', status: 'unavailable' }]
        : [{ id: 'oci', kind: 'oci', status: 'available' }],
      // The complete-population tally, which is deliberately LARGER than the bounded
      // `sources` list above it: the meta list is capped and least-healthy-biased, so
      // counting it would answer a different question.
      sourceCounts: partial
        ? { total: 7, available: 5, partial: 1, unavailable: 1 }
        : { total: 4, available: 4 },
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

function mountView(params: Record<string, string> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetOverview, { target, props: { refreshTick: 0, params } });
  return { target, component };
}

describe('FleetOverview — operational landing (scenarios 1-5)', () => {
  beforeEach(() => { overviewFn.mockReset(); entitiesFn.mockReset(); });

  it('splits the services line into declared and external, linking the external systems', async () => {
    const ov = baseOverview();
    ov.summary.externals = 4;
    ov.summary.hiddenExternals = 2;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="overview-services-line"]')).toBeTruthy());
    const line = target.querySelector('[data-testid="overview-services-line"]') as HTMLElement;
    expect(line.textContent?.replace(/\s+/g, ' ').trim()).toBe('7: 3 declared, 4 external (2 hidden)');
    expect(line.querySelector('[data-testid="overview-externals"]')?.getAttribute('href')).toBe('#/fleet/externals');
    expect(line.querySelector('[title]')?.getAttribute('title')).toBe('External systems only services you cannot see declare');
    unmount(component); document.body.removeChild(target);
  });

  it('adds the inferred segment so the parts add up to the headline, linking the inferred list', async () => {
    const ov = baseOverview();
    ov.summary.externals = 4;
    ov.summary.inferredServices = 5;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="overview-services-line"]')).toBeTruthy());
    const line = target.querySelector('[data-testid="overview-services-line"]') as HTMLElement;
    expect(line.textContent?.replace(/\s+/g, ' ').trim()).toBe('12: 3 declared, 5 inferred, 4 external');
    expect(line.querySelector('[data-testid="overview-inferred"]')?.getAttribute('href')).toBe('#/fleet/services?declaration=inferred');
    unmount(component); document.body.removeChild(target);
  });

  it('shows the services line for inferred services alone, with the unverified ones', async () => {
    const ov = baseOverview();
    ov.summary.inferredServices = 5;
    ov.summary.inferredUnverified = 2;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="overview-services-line"]')).toBeTruthy());
    const line = target.querySelector('[data-testid="overview-services-line"]') as HTMLElement;
    expect(line.textContent?.replace(/\s+/g, ' ').trim()).toBe('8: 3 declared, 5 inferred (2 unverified)');
    expect(line.querySelector('[data-testid="overview-externals"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('leaves a zero inferred segment out', async () => {
    const ov = baseOverview();
    ov.summary.externals = 4;
    ov.summary.inferredServices = 0;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="overview-services-line"]')).toBeTruthy());
    expect(target.querySelector('[data-testid="overview-inferred"]')).toBeNull();
    expect(target.querySelector('[data-testid="overview-services-line"]')?.textContent).not.toContain('inferred');
    unmount(component); document.body.removeChild(target);
  });

  it('a cluster with only inferred services is not an empty fleet', async () => {
    const ov = baseOverview();
    ov.summary.services = 0;
    ov.summary.inferredServices = 7;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="overview-services-line"]')).toBeTruthy());
    expect(target.querySelector('.empty-fleet')).toBeNull();
    expect(target.querySelector('[data-testid="overview-services-line"]')?.textContent?.replace(/\s+/g, ' ').trim()).toBe('7: 0 declared, 7 inferred');
    // No contract is checked, so the answer gives no verdict and no compliance bar
    // speaks about zero services.
    const answer = target.querySelector('[data-testid="overview-answer"]') as HTMLElement;
    expect(answer.textContent?.replace(/\s+/g, ' ').trim()).toBe('No contracts yet. Pacto sees 7 workloads as inferred services; a contract lets it check them.');
    expect(answer.querySelector('a')?.getAttribute('href')).toBe('#/fleet/services?declaration=inferred');
    expect(target.textContent).not.toContain('Nothing is broken');
    expect(target.textContent).not.toContain('No declared services yet.');
    unmount(component); document.body.removeChild(target);
  });

  it('keeps the plain services count when no external system is declared', async () => {
    overviewFn.mockResolvedValue(baseOverview());
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="trust-figure"] .fig-aside dl.facts')).toBeTruthy());
    expect(target.querySelector('[data-testid="overview-services-line"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('answers the trust figure with one compliance bar whose legend lists each bucket, and explains how Pacto knows on request', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    entitiesFn.mockResolvedValue({ meta: baseOverview(true).meta, total: 3, count: 0, entities: [], aggregate: { services: 3, serviceCompliance: { compliant: 2, nonCompliant: 1 } } });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="trust-figure"] .fig-aside .dist-title')).toBeTruthy());
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], limit: 1 }));
    const aside = target.querySelector('[data-testid="trust-figure"] .fig-aside') as HTMLElement;
    expect(aside.querySelector('.dist-title')?.textContent).toBe('What the snapshot says about 3 services');
    const hrefs = Array.from(aside.querySelectorAll('a')).map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual(expect.arrayContaining(['#/fleet/services?status=NonCompliant', '#/fleet/services', '#/fleet/revisions', '#/fleet/targets']));
    // Where the numbers came from: the provenance strip, partial in words.
    expect(target.querySelector('[data-testid="trust-figure"] [data-testid="provenance"]')?.textContent)
      .toContain('Partial: 1 of 7 sources unavailable, 1 partial');
    expect(target.querySelector('[data-testid="knows-explainer"]')).toBeNull();
    const toggle = target.querySelector('[data-testid="knows-toggle"]') as HTMLButtonElement;
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    toggle.click();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="knows-explainer"]')).toBeTruthy());
    const ex = target.querySelector('[data-testid="knows-explainer"]') as HTMLElement;
    expect(ex.querySelector('.fig-q')?.textContent).toBe('How does Pacto know?');
    expect(ex.querySelector('.fig-kind')?.textContent).toBe('How this works');
    expect(toggle.textContent).toBe('Hide how Pacto knows');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 1: loads the product overview and renders the summary', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => {
      expect(target.textContent).toContain('Is anything wrong?');
      expect(target.textContent).toContain('Revision-match certainty');
    });
    expect(overviewFn).toHaveBeenCalledTimes(1); // consumes /api/fleet/overview, not the snapshot
    expect(target.querySelector('.product-page > .page-hd.iso-backdrop')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('keeps the header plain while the overview is not ready', async () => {
    // Once: the beforeEach returns the mock, so vitest calls it again as a teardown.
    overviewFn.mockRejectedValueOnce(new ApiError(503, 'unavailable'));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent).toContain('Can’t reach the Pacto backend'));
    expect(target.querySelector('.iso-backdrop')).toBeFalsy();
    unmount(component); document.body.removeChild(target);
  });

  it('counts golden paths apart from services and links to the Golden paths tab', async () => {
    const o = baseOverview(false);
    o.summary.goldenPaths = 2;
    o.summary.invalidGoldenPaths = 1;
    overviewFn.mockResolvedValue(o);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="overview-golden-paths"]')).toBeTruthy());
    const tile = target.querySelector('[data-testid="overview-golden-paths"]') as HTMLAnchorElement;
    // An invalid golden path is listed first, least healthy first.
    expect(tile.getAttribute('href')).toBe('#/fleet/golden-paths?sort=health');
    expect(tile.textContent).toContain('2');
    expect(tile.textContent).toContain('Golden paths');
    expect(tile.textContent).toContain('1 with an invalid contract');
    unmount(component); document.body.removeChild(target);
  });

  it('shows no golden path tile when there is none', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent).toContain('Is anything wrong?'));
    expect(target.querySelector('[data-testid="overview-golden-paths"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 2: partial source + zero attention NEVER shows "All clear"', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    // Wait for the loaded content (the summary), not the always-present header.
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    const text = target.textContent || '';
    expect(text).not.toMatch(/all clear/i);
    expect(text).toContain('1 data source is unavailable'); // the incompleteness is shown honestly
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 3: complete + zero attention MAY show an all-clear state', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent).toMatch(/all clear/i));
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 4: a category tile links to its filter', async () => {
    const ov = baseOverview(false);
    ov.summary.servicesNeedingAttention = 1;
    ov.entryPoints = [{ label: 'Operational targets not compliant', count: 1, view: 'attention', category: 'non-compliant', href: '/fleet/attention?category=non-compliant' }];
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('a.tile')).toBeTruthy());
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

  it('a fleet holding only golden paths never says nothing has reported a contract', async () => {
    const ov = emptyOverview(false);
    ov.summary.goldenPaths = 3;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('.op-summary')).toBeTruthy());
    expect(target.querySelector('.empty-fleet')).toBeFalsy();
    expect(target.textContent || '').not.toMatch(/no services tracked/i);
    expect(target.querySelector('.ov-gp-count')?.textContent).toBe('3');
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
    expect(target.querySelector('.knowledge')).toBeTruthy();
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
    expect(target.querySelector('.knowledge')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('an `empty`-completeness fleet is honestly empty, NOT "sources degraded"', async () => {
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
    expect(target.querySelector('.knowledge')).toBeFalsy();
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
    // The exact-match count is the "Exact" bucket's own printed value, and it is stated
    // once: a sentence restating it under the bars put the same number on the page twice.
    expect(posture).toMatch(/Exact\s*3\b/);
    // A proportion is a way in, not a picture.
    // Revision-match buckets have no list to open: attention `unresolved` is about
    // dependencies, so Ambiguous stays plain text.
    expect(linkIn(target, 'ov-posture', 'Ambiguous')).toBeFalsy();
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

  // The backend names all seven canonical statuses (ComplianceTally); the overview
  // used to publish only five of the eight buckets, so a Warning or Reference target
  // — named correctly on the targets list — became a grey remainder here. Two Pacto
  // surfaces must not classify the same target differently.
  it('band 2 names a Warning target instead of leaving it unclassified', async () => {
    const ov = baseOverview(false);
    ov.summary.compliantTargets = 3;
    ov.summary.warningTargets = 1;
    ov.summary.referenceTargets = 1; // 5 targets, all accounted for
    ov.summary.evidence = { withEvidence: 5, withoutEvidence: 0, stale: 0, quarantined: 0 };
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('section[aria-labelledby="ov-posture"]')).toBeTruthy());
    const posture = sectionText(target, 'ov-posture');
    expect(posture).toContain('Warning');
    expect(posture).toContain('Reference');
    expect(posture).not.toContain('Unclassified');
    unmount(component); document.body.removeChild(target);
  });

  // Attention fires on NonCompliant / Unknown / stale, so a fleet of Warning and
  // Reference targets can legitimately reach zero attention with complete knowledge.
  // "All clear" may still be true of the BACKLOG; "every operational target is
  // compliant" is a different, universally-quantified claim, and it was being made
  // over a population the same payload shows is not all compliant.
  it('never claims every target is compliant when the distribution says otherwise', async () => {
    const ov = baseOverview(false);
    ov.summary.compliantTargets = 3;
    ov.summary.warningTargets = 2;
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.textContent).toMatch(/all clear/i));
    expect(target.textContent).not.toMatch(/every operational target is compliant/i);
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
    expect(org).toContain('Owners disagree');
    expect(linkIn(target, 'ov-org', 'Owners disagree')?.getAttribute('href'))
      .toBe('#/fleet/services?ownership=conflicting');
    expect(linkIn(target, 'ov-org', 'No declared owner')?.getAttribute('href'))
      .toBe('#/fleet/services?ownership=unowned');
    unmount(component); document.body.removeChild(target);
  });

  // A reader shown a fleet-wide ownership gap has to be able to get from that fact to
  // whose gap it is from right here, not only through the nav.
  it('band 3 is a door to Owners', async () => {
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
    expect(org).toContain('No declared services yet');
    expect(org).toContain('No contract revisions are tracked yet');
    unmount(component); document.body.removeChild(target);
  });
});

// SECTION 6: data sources are secondary, not hidden. They used to be an unlabeled
// caption under the immediate-situation counters -- no heading, absent from the page
// contents, and read as a footnote to the numbers above rather than as the answer to
// "where did any of this come from". Promoting it to a section is NOT promoting it to
// a fifth primary tab: the nav stays Overview / Services / Operational graph /
// Change analysis.
describe('FleetOverview — data sources are a findable section, not a caption', () => {
  beforeEach(() => overviewFn.mockReset());

  it('gives data sources their own heading, contents entry and way in', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-sources')).toBeTruthy());
    const band = target.querySelector('#sec-sources') as HTMLElement;
    // A real section: semantic heading, and the heading names the section.
    expect(band.querySelector('h2')?.textContent?.trim()).toBe('Data sources');
    expect(band.getAttribute('aria-labelledby')).toBe(band.querySelector('h2')?.id);
    // Discoverable by the page contents, which builds itself from [data-toc][id].
    expect(band.getAttribute('data-toc')).toBe('Data sources');
    // One obvious action that opens the complete inventory.
    const all = band.querySelector('a.ov-viewall') as HTMLAnchorElement;
    expect(all.getAttribute('href')).toBe('#/fleet/sources');
    expect(all.textContent?.trim()).toBe('View all data sources');
    unmount(component); document.body.removeChild(target);
  });

  it('names the sources and leaves their counts to the trust figure', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-sources')).toBeTruthy());
    const band = target.querySelector('#sec-sources') as HTMLElement;
    const text = (band.textContent || '').replace(/\s+/g, ' ');
    // One carrier per fact (L3): the figure at the top counts the 7 sources by state, so
    // this section states no second population total to reconcile with it.
    expect(text).not.toContain('7 data sources');
    expect(band.querySelectorAll('a.sh-chip')).toHaveLength(2);
    unmount(component); document.body.removeChild(target);
  });

  it('appears exactly once on the page', async () => {
    // The old caption lived inside the immediate-situation band. Adding a section
    // without removing it would have given one page two source summaries to reconcile.
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-sources')).toBeTruthy());
    expect(target.querySelectorAll('.source-health')).toHaveLength(1);
    expect((target.textContent || '').match(/Where this knowledge came from/g)).toBeNull();
    // ...and it did not become a fifth primary destination.
    expect(target.querySelector('.op-nav, nav.primary')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('says so plainly when every data source answered', async () => {
    overviewFn.mockResolvedValue(baseOverview(false));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-sources')).toBeTruthy());
    const text = ((target.querySelector('#sec-sources') as HTMLElement).textContent || '').replace(/\s+/g, ' ');
    // A healthy fleet gets a sentence, never a row of zeroes to scan.
    expect(text).not.toMatch(/0 (unavailable|stale|partial)/);
    unmount(component); document.body.removeChild(target);
  });
});

// The page title is a question; the line under it is the answer, and it never says
// "no" past what the snapshot knows.
describe('FleetOverview — the answer line', () => {
  beforeEach(() => overviewFn.mockReset());
  const answer = (t: HTMLElement) => (t.querySelector('[data-testid="overview-answer"]')?.textContent || '').replace(/\s+/g, ' ').trim();

  it('says yes and names the error-grade counts in plain words', async () => {
    const ov = baseOverview(false);
    ov.entryPoints = [
      { label: 'Operational targets not compliant', count: 229, severity: 'error', view: 'attention', category: 'non-compliant', href: '/fleet/attention?category=non-compliant' },
      { label: 'Invalid revisions', count: 1, severity: 'error', view: 'attention', category: 'invalid', href: '/fleet/attention?category=invalid' },
      { label: 'Operational targets with stale evidence', count: 5, severity: 'warning', view: 'attention', category: 'stale', href: '/fleet/attention?category=stale' },
    ];
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(answer(target)).not.toBe(''));
    expect(answer(target)).toBe('Yes. 229 running targets break their contract and 1 revision is invalid.');
    unmount(component); document.body.removeChild(target);
  });

  it('says where the broken targets run, breaks them down by environment and names the missing sources', async () => {
    const ov = baseOverview(true);
    ov.summary.byScope = [
      { scope: 'prod', targets: 120, nonCompliant: 48, unknown: 2, stale: 1 },
      { scope: 'staging', targets: 40, nonCompliant: 3, unknown: 0, stale: 0 },
    ];
    ov.summary.scopes = 2;
    ov.entryPoints = [
      { label: 'Operational targets not compliant', count: 51, severity: 'error', view: 'attention', category: 'non-compliant', href: '/fleet/attention?category=non-compliant' },
    ];
    overviewFn.mockResolvedValue(ov);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(answer(target)).not.toBe(''));
    expect(answer(target)).toBe('Yes. 51 running targets break their contract. 48 of the targets that break their contract run in prod.');
    const rows = [...target.querySelectorAll('[data-testid="overview-by-scope"] tbody tr')].map((r) => [...r.children].map((c) => (c.textContent || '').trim()).join(' '));
    expect(rows).toEqual(['prod 120 48 2 1', 'staging 40 3 0 0']);
    const banner = target.querySelector('.knowledge') as HTMLElement;
    expect(banner.className).toContain('tone-warn');
    expect(banner.textContent).toContain('(k8s)');
    unmount(component); document.body.removeChild(target);
  });

  it('does not claim all clear when data is missing', async () => {
    overviewFn.mockResolvedValue(baseOverview(true));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(answer(target)).not.toBe(''));
    expect(answer(target)).toBe('Nothing broken found.');
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetOverview — operational health and knowledge', () => {
  beforeEach(() => { overviewFn.mockReset(); entitiesFn.mockReset(); });
  const say = (t: HTMLElement) => t.querySelector('[data-testid="overview-answer"]')?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

  function withHealth(over: Record<string, number> = {}) {
    const o = baseOverview(false);
    o.summary.operationalHealth = { available: 5, degraded: 0, unavailable: 0, unknown: 0, notObserved: 0, ...over };
    o.summary.knowledge = { services: 3, described: 1, withGaps: 1, unknown: 1, gaps: { noSummary: 1, noDomain: 0, noInterfaceDocs: 0, noReadiness: 1, notDeployed: 1, unverifiedCapability: 0 } };
    o.summary.coverage = { evaluated: 12, required: 20, reporting: 4, notReporting: 1 };
    return o;
  }

  it('draws a health band over every target, failing linked to its triage, with a caption from the same tally', async () => {
    overviewFn.mockResolvedValue(withHealth({ available: 2, unavailable: 1, unknown: 2 }));
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-health')).toBeTruthy());
    const band = target.querySelector('#sec-health') as HTMLElement;
    expect(band.getAttribute('data-toc')).toBe('Operational health');
    expect(band.textContent).toContain('All 5 operational targets');
    expect(Array.from(band.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toContain('#/fleet/attention?category=health');
    expect(band.querySelector('[data-testid="health-caption"]')?.textContent)
      .toBe('1 target fails its health check, Pacto cannot tell the health of 2 targets and 2 targets are healthy.');
    unmount(component); document.body.removeChild(target);
  });

  it('draws a knowledge band, one row per gap kind linking with gap=, and the coverage sentence', async () => {
    overviewFn.mockResolvedValue(withHealth());
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-knowledge')).toBeTruthy());
    const band = target.querySelector('#sec-knowledge') as HTMLElement;
    const rows = Array.from(band.querySelectorAll('[data-testid="knowledge-gaps"] a'));
    // Kinds no service has are not listed.
    expect(rows.map((a) => [a.querySelector('.ov-gap-label')?.textContent, a.querySelector('.ov-gap-count')?.textContent, a.getAttribute('href')])).toEqual([
      ['No summary', '1', '#/fleet/services?gap=no-summary'],
      ['No readiness assessment', '1', '#/fleet/services?gap=no-readiness'],
      ['Runs nowhere', '1', '#/fleet/services?gap=not-deployed'],
    ]);
    expect(Array.from(band.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toContain('#/fleet/services?gap=unknown');
    expect(band.querySelector('[data-testid="knowledge-caption"]')?.textContent)
      .toBe('1 service leaves something out of the contract, Pacto cannot tell for 1 service and 1 service is fully described.');
    const cov = band.querySelector('[data-testid="coverage-sentence"]') as HTMLElement;
    expect(cov.textContent?.replace(/\s+/g, ' ').trim()).toBe('12 of 20 required assertions evaluated on 4 targets; 1 reports no coverage.');
    // No list filters targets by coverage, so the count is plain text, never a link to every target.
    expect(cov.querySelector('a')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('draws the owner-unknown bucket hatched in the ownership bar', async () => {
    const o = withHealth();
    o.summary.ownership = { consistent: 1, conflicting: 0, unowned: 1, unknown: 1 };
    overviewFn.mockResolvedValue(o);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-org')).toBeTruthy());
    expect(Array.from(target.querySelectorAll('#sec-org a')).map((a) => a.getAttribute('href'))).toContain('#/fleet/services?ownership=unknown');
    expect(target.querySelector('#sec-org')?.textContent).toContain('Owner unknown');
    unmount(component); document.body.removeChild(target);
  });

  it('never says all clear with a failing or an unknown health check', async () => {
    overviewFn.mockResolvedValue(withHealth({ available: 4, unknown: 1 }));
    let m = mountView();
    await vi.waitFor(() => expect(say(m.target)).not.toBe(''));
    expect(say(m.target)).toBe('Nothing broken found. Pacto cannot tell the health of 1 operational target.');
    unmount(m.component); document.body.removeChild(m.target);

    const failing = withHealth({ available: 4, unavailable: 1 });
    failing.entryPoints.push({ label: 'Operational targets failing their health check', count: 1, view: 'attention', category: 'health', severity: 'warning', href: '/fleet/attention?category=health' });
    failing.attention.total = 1;
    overviewFn.mockResolvedValue(failing);
    m = mountView();
    await vi.waitFor(() => expect(say(m.target)).not.toBe(''));
    expect(say(m.target)).toBe('Yes. 1 running target fails its health check.');
    unmount(m.component); document.body.removeChild(m.target);

    overviewFn.mockResolvedValue(withHealth());
    m = mountView();
    await vi.waitFor(() => expect(say(m.target)).toMatch(/all clear/i));
    unmount(m.component); document.body.removeChild(m.target);
  });

  it('splits stale compliant targets off Compliant in the posture bar', async () => {
    const o = withHealth();
    o.summary.staleCompliantTargets = 2;
    overviewFn.mockResolvedValue(o);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-posture')).toBeTruthy());
    const posture = target.querySelector('#sec-posture') as HTMLElement;
    expect(posture.textContent).toContain('Compliant, evidence stale');
    // The segment opens the stale list narrowed to compliant targets: exactly what it counts.
    const seg = Array.from(posture.querySelectorAll('a')).find((a) => (a.textContent || '').includes('Compliant, evidence stale'));
    expect(seg?.getAttribute('href')).toBe('#/fleet/attention?category=stale&status=Compliant');
    unmount(component); document.body.removeChild(target);
  });
});

describe('FleetOverview — the fleet map and its two figures (509-S12)', () => {
  beforeEach(() => { overviewFn.mockReset(); entitiesFn.mockReset(); });

  const counts = (matched: number, expectedOnly: number, observedOnly: number, insufficient: number) => ({ matched, expectedOnly, observedOnly, insufficient });
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
  function withFigures(): any {
    const o = baseOverview(false);
    o.summary.match = { services: counts(60, 10, 5, 20), dependencies: counts(25, 0, 0, 10), percent: 85, coverage: 76, completeness: 'complete' };
    o.summary.knowledge = {
      services: 3, described: 1, withGaps: 1, unknown: 1, gaps: { noSummary: 1, noDomain: 0, noInterfaceDocs: 0, noReadiness: 0, notDeployed: 0, unverifiedCapability: 0 },
      contractCompleteness: { percentComplete: 72, judged: 2950, unknown: 50, fullyDescribed: 1204, mostMissing: [{ section: 'configuration', count: 812 }, { section: 'readiness', count: 640 }, { section: 'interfaces', count: 300 }] },
    };
    return o;
  }
  const bucket = (since: string, coverage?: unknown) => ({ since, until: since, ...(coverage ? { coverage } : {}) });
  const record = (sm: number, pct: number) => ({
    completeness: 'complete', services: 10, snapshotAsOf: '2026-11-01T00:00:00Z', uncontracted: 0, links: {},
    match: { servicesMatched: sm, servicesExpectedOnly: 1, servicesObservedOnly: 1, servicesInsufficient: 2, dependenciesMatched: 0, dependenciesExpectedOnly: 0, dependenciesObservedOnly: 0, dependenciesInsufficient: 0 },
    contracts: { services: 10, judged: 10, unknown: 0, fullyDescribed: 5, percentComplete: pct },
  });
  const text = (t: Element | null | undefined) => t?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

  it('leads with two figures, their sub-lines and keys, and their weekly trend', async () => {
    overviewFn.mockResolvedValue(withFigures());
    statsFn.mockResolvedValue({ buckets: [bucket('2026-10-20T00:00:00Z', record(6, 60)), bucket('2026-10-27T00:00:00Z'), bucket('2026-11-03T00:00:00Z', record(8, 70))], limitations: [] });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="figure-match-trend"]')).toBeTruthy());
    expect(statsFn).toHaveBeenCalledWith({ window: '90d' });
    const figs = target.querySelectorAll('[data-testid="fleet-figures"] > .ff-tile');
    expect(figs).toHaveLength(2);
    expect(text(target.querySelector('[data-testid="figure-match-value"]'))).toBe('85%of what Pacto can observe');
    expect(text(target.querySelector('[data-testid="figure-match-sub"]'))).toBe('76% observed coverage · unknown never counts as matched');
    expect(text(target.querySelector('[data-testid="figure-completeness-value"]'))).toBe('72%1,204 of 2,950 judged services fully described, 50 not judged');
    expect(text(target.querySelector('[data-testid="figure-completeness-sub"]'))).toBe('Most missing: configuration schema 812 · readiness 640 · interface documents 300');
    const keys = text(target.querySelector('[data-testid="figure-match"]'));
    for (const k of ['Matched', 'Expected, not observed', 'Observed, not expected', 'Insufficient evidence']) expect(keys).toContain(k);
    expect(target.querySelector('[data-testid="figure-completeness-trend"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });

  it('says why the trend is missing for a reader who cannot see every service', async () => {
    overviewFn.mockResolvedValue(withFigures());
    statsFn.mockResolvedValue({ buckets: [bucket('2026-10-20T00:00:00Z'), bucket('2026-10-27T00:00:00Z')], limitations: [{ code: 'COVERAGE_OUTSIDE_YOUR_VIEW', message: 'x' }] });
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="figure-match-trend-note"]')).toBeTruthy());
    expect(text(target.querySelector('[data-testid="figure-match-trend-note"]'))).toBe('The trend counts every service, so only a reader who can see every service sees it.');
    expect(target.querySelector('[data-testid="figure-match-trend"]')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('never shows a match percentage when Pacto observes nothing', async () => {
    const o = withFigures();
    o.summary.match = { services: counts(0, 0, 0, 3), dependencies: counts(0, 0, 0, 0), completeness: 'complete' };
    overviewFn.mockResolvedValue(o);
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="figure-match-value"]')).toBeTruthy());
    expect(text(target.querySelector('[data-testid="figure-match-value"]'))).toBe('none yet');
    expect(text(target.querySelector('[data-testid="figure-match-sub"]'))).toBe('Pacto observes nothing yet, so nothing can match.');
    unmount(component); document.body.removeChild(target);
  });

  it('mounts the map with its state, the rail open, and the match figure toggles the gaps', async () => {
    overviewFn.mockResolvedValue(withFigures());
    location.hash = '#/fleet?groupBy=team';
    const { target, component } = mountView({ groupBy: 'team', rail: 'open', category: 'health' });
    await vi.waitFor(() => expect(mapFn).toHaveBeenCalled());
    expect(mapFn.mock.calls[0][0]).toMatchObject({ groupBy: 'team' });
    const map = target.querySelector('[data-testid="fleet-map"]') as HTMLElement;
    expect(map.classList.contains('fm-full')).toBe(false);
    expect(target.querySelector('[data-testid="fleet-map-rail"]')).toBeTruthy();
    (target.querySelector('[data-testid="figure-match"] button') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet?groupBy=team&gaps=all');
    unmount(component); document.body.removeChild(target);
    location.hash = '';
  });

  it('has no Needs attention section: the list sits beside the map', async () => {
    overviewFn.mockResolvedValue(withFigures());
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-knowledge')).toBeTruthy());
    expect(target.querySelector('#sec-attention')).toBeNull();
    expect(target.querySelector('[data-toc="Needs attention"]')).toBeNull();
    expect(target.querySelector('.attn-item')).toBeNull();
    unmount(component); document.body.removeChild(target);
  });

  it('retitles Knowledge Contract completeness, with the checklist and the most missing sections as links', async () => {
    overviewFn.mockResolvedValue(withFigures());
    const { target, component } = mountView();
    await vi.waitFor(() => expect(target.querySelector('#sec-knowledge')).toBeTruthy());
    const band = target.querySelector('#sec-knowledge') as HTMLElement;
    expect(band.getAttribute('data-toc')).toBe('Contract completeness');
    expect(text(band.querySelector('h2'))).toBe('Contract completeness');
    expect(band.textContent).toContain('Fully described');
    const rows = Array.from(band.querySelectorAll('[data-testid="completeness-most-missing"] a'));
    expect(rows.map((a) => [text(a.querySelector('.ov-gap-label') as HTMLElement), a.getAttribute('href')])).toEqual([
      ['configuration schema', '#/fleet?missing=configuration'],
      ['readiness', '#/fleet/services?gap=no-readiness'],
      ['interface documents', '#/fleet/services?gap=no-interface-docs'],
    ]);
    // The knowledge gaps it grew from are all still there.
    expect(band.querySelector('[data-testid="knowledge-gaps"]')).toBeTruthy();
    unmount(component); document.body.removeChild(target);
  });
});
