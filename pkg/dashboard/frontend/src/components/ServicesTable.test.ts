/**
 * Component render tests for ServicesTable.svelte.
 * Verifies table renders one row per service with all columns, click-to-filter facets.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ServicesTable from './ServicesTable.svelte';

describe('ServicesTable — columns and click-to-filter', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  it('renders one row per service', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', version: '1.0.0', contractStatus: 'Compliant' },
          { name: 'svc-b', version: '2.0.0', contractStatus: 'Warning' },
        ],
      },
    });

    const rows = target.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(2);

    unmount(component);
  });

  it('renders name column with service link', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'test-service', version: '1.0.0' },
        ],
      },
    });

    const nameLink = target.querySelector('a[href*="test-service"]');
    expect(nameLink).toBeTruthy();
    expect(nameLink?.textContent).toContain('test-service');

    unmount(component);
  });

  it('renders owner column with owner link', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', owner: { team: 'platform' } },
        ],
      },
    });

    const ownerLink = target.querySelector('a[href*="owners"]');
    expect(ownerLink).toBeTruthy();
    expect(ownerLink?.textContent).toContain('platform');

    unmount(component);
  });

  it('renders readiness score', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          {
            name: 'svc-a',
            readiness: {
              score: 85,
              minScore: 80,
              passing: true,
              checks: [],
            },
          },
        ],
      },
    });

    const text = target.textContent || '';
    expect(text).toContain('85');
    expect(text).toContain('%');

    unmount(component);
  });

  it('colors readiness by the gate (green + ✓ + tooltip when passing)', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-pass', readiness: { score: 79, minScore: 75, passing: true, expired: false, checks: [] } },
        ],
      },
    });

    const score = target.querySelector('.readiness-score')!;
    expect(score).toBeTruthy();
    expect(score.classList.contains('score-ok')).toBe(true);
    expect(score.getAttribute('data-tip')).toBe('79% — passing (minScore 75)');
    // Explicit gate check mark.
    expect(score.querySelector('.gate-check')).toBeTruthy();

    unmount(component);
  });

  it('colors readiness amber + no ✓ when below the gate, even at a similar score', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-fail', readiness: { score: 70, minScore: 80, passing: false, expired: false, checks: [] } },
        ],
      },
    });

    const score = target.querySelector('.readiness-score')!;
    expect(score.classList.contains('score-ok')).toBe(false);
    expect(score.classList.contains('score-warn')).toBe(true);
    expect(score.getAttribute('data-tip')).toBe('70% — below gate (minScore 80)');
    expect(score.querySelector('.gate-check')).toBeNull();

    unmount(component);
  });

  it('renders contract status badge', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', contractStatus: 'Compliant' },
        ],
      },
    });

    const badge = target.querySelector('.badge');
    expect(badge).toBeTruthy();
    expect(badge?.textContent).toContain('Compliant');

    unmount(component);
  });

  it('renders compliance score', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', complianceScore: 92 },
        ],
      },
    });

    const text = target.textContent || '';
    expect(text).toContain('92');
    expect(text).toContain('%');

    unmount(component);
  });

  it('renders blast radius', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', blastRadius: 5 },
        ],
      },
    });

    const text = target.textContent || '';
    expect(text).toContain('5');

    unmount(component);
  });

  it('renders checks column', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', checksPassed: 8, checksTotal: 10, checksFailed: 2 },
        ],
      },
    });

    const text = target.textContent || '';
    expect(text).toContain('8');
    expect(text).toContain('10');

    unmount(component);
  });

  it('renders source dots', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', sources: ['k8s', 'oci'] },
        ],
      },
    });

    const sourceDots = target.querySelectorAll('.source-dot');
    expect(sourceDots.length).toBeGreaterThan(0);

    unmount(component);
  });

  it('renders a colgroup with one col per column for the fixed layout', () => {
    const component = mount(ServicesTable, {
      target,
      props: {
        services: [
          { name: 'svc-a', version: '1.0.0', contractStatus: 'Compliant' },
        ],
      },
    });

    // table-layout:fixed sizes columns from this <colgroup> (one <col> per
    // column), which is what stops the nowrap columns from over-growing the
    // table and triggering a spurious horizontal scrollbar.
    const cols = target.querySelectorAll('table > colgroup > col');
    expect(cols).toHaveLength(8);
    // The Name column flexes; the rest are compact fixed-ish widths.
    expect(target.querySelector('col.col-name')).toBeTruthy();
    expect(target.querySelector('col.col-source')).toBeTruthy();

    unmount(component);
  });
});
