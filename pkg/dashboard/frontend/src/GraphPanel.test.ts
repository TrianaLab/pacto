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
});
