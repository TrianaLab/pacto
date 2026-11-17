import { describe, it, expect, beforeEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import GraphPanel from './GraphPanel.svelte';

describe('GraphPanel', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  it('renders zoom + legend when enabled', () => {
    const c = mount(GraphPanel, {
      target,
      props: {
        graphData: { nodes: [{ name: 'a', id: 'a', serviceName: 'a', status: 'ok', edges: [] }] },
        showZoom: true,
        showLegend: true,
      },
    });
    expect(target.querySelector('.graph-controls')).not.toBeNull();
    expect(target.querySelector('.graph-legend')).not.toBeNull();
    unmount(c);
  });

  it('hides zoom and legend when disabled', () => {
    const c = mount(GraphPanel, {
      target,
      props: {
        graphData: { nodes: [{ name: 'a', id: 'a', serviceName: 'a', status: 'ok', edges: [] }] },
        showZoom: false,
        showLegend: false,
      },
    });
    expect(target.querySelector('.graph-controls')).toBeNull();
    expect(target.querySelector('.graph-legend')).toBeNull();
    unmount(c);
  });

  it('tells external systems apart from unresolved refs in the legend', () => {
    const c = mount(GraphPanel, {
      target,
      props: { graphData: { nodes: [{ id: 'a', serviceName: 'a', status: 'ok', edges: [] }] }, showLegend: true },
    });
    const ext = target.querySelector('[data-legend="external"]')!;
    const unres = target.querySelector('[data-legend="unresolved"]')!;
    expect(ext.textContent?.trim()).toBe('External system');
    expect(unres.textContent?.trim()).toBe('Unresolved ref');
    expect(ext.querySelector('.legend-dot-ring')).not.toBeNull();
    expect(unres.querySelector('.legend-dot-ring')).toBeNull();
    expect([...target.querySelectorAll('.legend-item')].some((i) => i.textContent?.trim() === 'External')).toBe(false);
    unmount(c);
  });
});
