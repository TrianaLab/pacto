/**
 * Component render tests for DependenciesSection.svelte.
 * Verifies the lock/drift UI: pinned version/digest cell, drift badge, and the no-lock fallback.
 */
import { describe, it, expect, beforeEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import DependenciesSection from './DependenciesSection.svelte';

describe('DependenciesSection — lock and drift rendering', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  it('renders pinned cell with both lockedVersion and lockedDigest', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-a',
            ref: 'oci://ghcr.io/org/dep-a:1.0',
            required: true,
            compatibility: '^1.0',
            lockedVersion: '1.2.3',
            lockedDigest: 'sha256:abcdef1234567890',
            driftStatus: 'locked',
          },
        ],
      },
    });

    const lockCell = target.querySelector('.lock-cell');
    expect(lockCell).toBeTruthy();
    expect(lockCell?.textContent).toContain('1.2.3');
    expect(lockCell?.textContent).toContain('@abcdef12'); // short digest
    expect(lockCell?.querySelector('.lock-glyph')).toBeTruthy();

    unmount(component);
    document.body.removeChild(target);
  });

  it('renders pinned cell with lockedDigest only', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-b',
            ref: 'oci://ghcr.io/org/dep-b',
            required: false,
            lockedDigest: 'sha256:fedcba0987654321',
            driftStatus: 'locked',
          },
        ],
      },
    });

    const lockCell = target.querySelector('.lock-cell');
    expect(lockCell).toBeTruthy();
    expect(lockCell?.textContent).toContain('@fedcba09'); // short digest
    expect(lockCell?.textContent).not.toContain('undefined');

    unmount(component);
    document.body.removeChild(target);
  });

  it('renders pinned cell with lockedVersion only', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-c',
            ref: 'oci://ghcr.io/org/dep-c:2.0',
            required: true,
            lockedVersion: '2.0.1',
            driftStatus: 'locked',
          },
        ],
      },
    });

    const lockCell = target.querySelector('.lock-cell');
    expect(lockCell).toBeTruthy();
    expect(lockCell?.textContent).toContain('2.0.1');
    expect(lockCell?.textContent).not.toContain('@'); // no digest

    unmount(component);
    document.body.removeChild(target);
  });

  it('renders drift badge when driftStatus is drift', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-drift',
            ref: 'oci://ghcr.io/org/dep:1.0',
            required: true,
            lockedVersion: '1.0.0',
            lockedDigest: 'sha256:abc123',
            driftStatus: 'drift',
          },
        ],
      },
    });

    const lockCell = target.querySelector('.lock-cell');
    expect(lockCell).toBeTruthy();
    const driftBadge = lockCell?.querySelector('.badge');
    expect(driftBadge).toBeTruthy();
    expect(driftBadge?.textContent).toContain('Drift');

    unmount(component);
    document.body.removeChild(target);
  });

  it('does not render drift badge for locked status', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-locked',
            ref: 'oci://ghcr.io/org/dep:1.0',
            required: true,
            lockedVersion: '1.0.0',
            driftStatus: 'locked',
          },
        ],
      },
    });

    const lockCell = target.querySelector('.lock-cell');
    expect(lockCell).toBeTruthy();
    const driftBadge = lockCell?.querySelector('.badge');
    expect(driftBadge).toBeNull();

    unmount(component);
    document.body.removeChild(target);
  });

  it('does not render drift badge for unlocked status', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-unlocked',
            ref: 'oci://ghcr.io/org/dep:1.0',
            required: false,
            driftStatus: 'unlocked',
          },
        ],
      },
    });

    const cells = target.querySelectorAll('td');
    const pinnedCell = cells[cells.length - 1]; // last column
    expect(pinnedCell.textContent?.trim()).toBe('—');

    unmount(component);
    document.body.removeChild(target);
  });

  it('does not render drift badge for unknown status', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-unknown',
            ref: 'oci://ghcr.io/org/dep:1.0',
            required: true,
            lockedVersion: '1.0.0',
            driftStatus: 'unknown',
          },
        ],
      },
    });

    const lockCell = target.querySelector('.lock-cell');
    expect(lockCell).toBeTruthy();
    const driftBadge = lockCell?.querySelector('.badge');
    expect(driftBadge).toBeNull();

    unmount(component);
    document.body.removeChild(target);
  });

  it('renders neutral fallback when no lock fields present', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-no-lock',
            ref: 'oci://ghcr.io/org/dep:latest',
            required: true,
            compatibility: '*',
          },
        ],
      },
    });

    const cells = target.querySelectorAll('td');
    const pinnedCell = cells[cells.length - 1]; // last column
    expect(pinnedCell.textContent?.trim()).toBe('—');
    expect(pinnedCell.querySelector('.lock-cell')).toBeNull();

    unmount(component);
    document.body.removeChild(target);
  });

  it('does not throw when rendering dependency without lock', () => {
    expect(() => {
      const component = mount(DependenciesSection, {
        target,
        props: {
          name: 'test-service',
          dependencies: [
            {
              name: 'dep-minimal',
              ref: 'oci://ghcr.io/org/dep:1.0',
              required: false,
            },
          ],
        },
      });
      unmount(component);
    }).not.toThrow();

    document.body.removeChild(target);
  });

  it('renders the GraphPanel toolbar (direction + depth) when graphData is present', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'root-svc',
        dependencies: [{ name: 'dep-a', ref: 'oci://ghcr.io/org/dep-a:1.0', required: true }],
        graphData: {
          nodes: [
            { id: 'root-svc', serviceName: 'root-svc', name: 'root-svc', status: 'ok', edges: [] },
          ],
        },
      },
    });

    // GraphPanel owns the direction/depth toolbar (showDirectionDepth); confirm it renders.
    expect(target.querySelector('.dep-graph-toolbar')).toBeTruthy();
    expect(target.querySelector('.graph-controls')).toBeTruthy();
    expect(target.querySelector('.graph-legend')).toBeTruthy();
    expect(target.textContent).toContain('Depth');

    unmount(component);
    document.body.removeChild(target);
  });

  it('renders multiple dependencies with mixed lock states', () => {
    const component = mount(DependenciesSection, {
      target,
      props: {
        name: 'test-service',
        dependencies: [
          {
            name: 'dep-locked',
            ref: 'oci://ghcr.io/org/a:1.0',
            required: true,
            lockedVersion: '1.0.0',
            lockedDigest: 'sha256:aaa',
            driftStatus: 'locked',
          },
          {
            name: 'dep-drift',
            ref: 'oci://ghcr.io/org/b:2.0',
            required: true,
            lockedVersion: '2.0.0',
            driftStatus: 'drift',
          },
          {
            name: 'dep-no-lock',
            ref: 'oci://ghcr.io/org/c:latest',
            required: false,
          },
        ],
        services: [], // provide empty services to avoid external badges
      },
    });

    const rows = target.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(3);

    const lockCells = target.querySelectorAll('.lock-cell');
    expect(lockCells).toHaveLength(2); // first two deps have locks

    // Find drift badges within lock cells specifically
    let driftBadgeCount = 0;
    lockCells.forEach(cell => {
      if (cell.querySelector('.badge')) driftBadgeCount++;
    });
    expect(driftBadgeCount).toBe(1); // only second dep has drift

    unmount(component);
    document.body.removeChild(target);
  });
});
