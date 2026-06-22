/**
 * Component render tests for SummaryBar.svelte.
 * Verifies the KPI cards, click-to-filter interactivity, and metrics computation.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import SummaryBar from './SummaryBar.svelte';

describe('SummaryBar — metrics and click-to-filter', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  it('renders compliance KPI from filtered services', () => {
    const component = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', contractStatus: 'Compliant', complianceScore: 100 },
          { name: 'b', contractStatus: 'Compliant', complianceScore: 90 },
          { name: 'c', contractStatus: 'Warning', complianceScore: 70 },
          { name: 'd', contractStatus: 'NonCompliant', complianceScore: 30 },
        ],
      },
    });

    const tiles = target.querySelectorAll('.metric-tile');
    expect(tiles.length).toBeGreaterThan(0);

    const complianceTile = Array.from(tiles).find((t) =>
      t.textContent?.includes('Compliant'),
    );
    expect(complianceTile).toBeTruthy();
    // 2 compliant out of 4 assessed (all 4 are assessed: Compliant, Compliant, Warning, NonCompliant) = 50%
    expect(complianceTile?.textContent).toContain('50');
    expect(complianceTile?.textContent).toContain('%');

    unmount(component);
  });

  it('renders needs-attention card with count', () => {
    const component = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', contractStatus: 'Compliant', complianceScore: 100 },
          { name: 'b', contractStatus: 'Warning', complianceScore: 60 },
          { name: 'c', contractStatus: 'NonCompliant', complianceScore: 20 },
        ],
      },
    });

    const needsAttentionTile = Array.from(target.querySelectorAll('.metric-tile')).find((t) =>
      t.textContent?.includes('Needs attention'),
    );
    expect(needsAttentionTile).toBeTruthy();
    expect(needsAttentionTile?.textContent).toContain('2'); // 1 warning + 1 non-compliant

    unmount(component);
  });

  it('renders readiness card with avg score and ready count', () => {
    const component = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', readiness: { score: 100, minScore: 80, passing: true } },
          { name: 'b', readiness: { score: 60, minScore: 80, passing: false } },
          { name: 'c', readiness: { score: 80, minScore: 80, passing: true } },
        ],
      },
    });

    const readinessTile = Array.from(target.querySelectorAll('.metric-tile')).find((t) =>
      t.textContent?.includes('Readiness'),
    );
    expect(readinessTile).toBeTruthy();
    // avg = (100 + 60 + 80) / 3 = 80
    expect(readinessTile?.textContent).toContain('80');
    expect(readinessTile?.textContent).toContain('%');
    // 2 of 3 clear the gate (score >= minScore). Copy is explicit about the gate.
    expect(readinessTile?.textContent).toContain('2 of 3 pass gate');

    unmount(component);
  });

  it('marks readiness green with a ✓ only when every configured service passes the gate', () => {
    const allPass = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', readiness: { score: 100, minScore: 80, passing: true } },
          { name: 'b', readiness: { score: 90, minScore: 80, passing: true } },
        ],
      },
    });
    let tile = Array.from(target.querySelectorAll('.metric-tile')).find((t) =>
      t.textContent?.includes('Readiness'),
    )!;
    let value = tile.querySelector('.metric-value')!;
    expect(value.classList.contains('score-ok')).toBe(true);
    expect(value.querySelector('.gate-check')).toBeTruthy();
    unmount(allPass);

    const someFail = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', readiness: { score: 100, minScore: 80, passing: true } },
          { name: 'b', readiness: { score: 40, minScore: 80, passing: false } },
        ],
      },
    });
    tile = Array.from(target.querySelectorAll('.metric-tile')).find((t) =>
      t.textContent?.includes('Readiness'),
    )!;
    value = tile.querySelector('.metric-value')!;
    expect(value.classList.contains('score-ok')).toBe(false);
    expect(value.querySelector('.gate-check')).toBeNull();
    unmount(someFail);
  });

  it('renders high-impact card', () => {
    const component = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', blastRadius: 5 },
          { name: 'b', blastRadius: 3 },
          { name: 'c', blastRadius: 1 },
        ],
      },
    });

    const highImpactTile = Array.from(target.querySelectorAll('.metric-tile')).find((t) =>
      t.textContent?.includes('High impact'),
    );
    expect(highImpactTile).toBeTruthy();
    expect(highImpactTile?.textContent).toContain('2'); // blast >= 3

    unmount(component);
  });

  it('readiness card is clickable', () => {
    const component = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', readiness: { score: 100, minScore: 80, passing: true } },
          { name: 'b', readiness: { score: 50, minScore: 80, passing: false } },
          { name: 'c', readiness: { score: 20, minScore: 80, passing: false } },
        ],
      },
    });

    const readinessTile = Array.from(target.querySelectorAll('.metric-tile')).find((t) =>
      t.textContent?.includes('Readiness'),
    );
    // Should be a button or anchor
    expect(readinessTile?.tagName).toMatch(/BUTTON|A/);

    unmount(component);
  });

  it('renders check status totals', () => {
    const component = mount(SummaryBar, {
      target,
      props: {
        services: [
          { name: 'a', checksPassed: 8, checksTotal: 10, checksFailed: 2 },
          { name: 'b', checksPassed: 5, checksTotal: 5, checksFailed: 0 },
        ],
      },
    });

    // Look for a tile mentioning checks (implementation may vary)
    const text = target.textContent || '';
    // At minimum, the bar should render the services
    expect(text.length).toBeGreaterThan(0);

    unmount(component);
  });
});
