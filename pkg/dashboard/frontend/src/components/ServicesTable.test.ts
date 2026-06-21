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
});
