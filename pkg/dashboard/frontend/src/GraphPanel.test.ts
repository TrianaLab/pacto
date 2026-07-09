import { describe, it, expect, beforeEach } from 'vitest';
import { mount, unmount } from 'svelte';
import GraphPanel from './GraphPanel.svelte';

describe('GraphPanel', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  it('renders zoom + legend when enabled, direction/depth when enabled', () => {
    const c = mount(GraphPanel, {
      target,
      props: {
        graphData: { nodes: [{ name: 'a', id: 'a', serviceName: 'a', status: 'ok', edges: [] }] },
        showZoom: true,
        showLegend: true,
        showDirectionDepth: true,
      },
    });
    expect(target.querySelector('.graph-controls')).not.toBeNull();
    expect(target.querySelector('.graph-legend')).not.toBeNull();
    expect(target.textContent).toContain('Depth');
    unmount(c);
  });

  it('hides toolbar and legend when disabled', () => {
    const c = mount(GraphPanel, {
      target,
      props: {
        graphData: { nodes: [{ name: 'a', id: 'a', serviceName: 'a', status: 'ok', edges: [] }] },
        showZoom: false,
        showLegend: false,
        showDirectionDepth: false,
      },
    });
    expect(target.querySelector('.graph-controls')).toBeNull();
    expect(target.querySelector('.graph-legend')).toBeNull();
    expect(target.textContent).not.toContain('Depth');
    unmount(c);
  });
});
