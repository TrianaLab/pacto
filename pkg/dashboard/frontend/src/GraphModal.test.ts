/**
 * Component tests for GraphModal.svelte — the full-screen dependency graph.
 * Verifies open/closed rendering, the controls toggle, and close via the button.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import GraphModal from './GraphModal.svelte';

const graphData = { nodes: [
  { id: 'root', serviceName: 'root', status: 'Compliant', edges: [{ targetId: 'a' }] },
  { id: 'a', serviceName: 'a', status: 'Compliant', edges: [] },
] };

describe('GraphModal', () => {
  let target: HTMLElement;
  beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
  afterEach(() => { target.remove(); });

  it('renders nothing when closed', () => {
    const c = mount(GraphModal, { target, props: { open: false, graphData, focusId: 'root' } });
    expect(document.querySelector('.graph-modal-backdrop')).toBeFalsy();
    unmount(c);
  });

  it('renders a dialog with the graph when open', () => {
    const c = mount(GraphModal, { target, props: { open: true, graphData, focusId: 'root' } });
    flushSync(); // let GraphCanvas's $effect run so the graph mounts
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).toBeTruthy();
    expect(dialog?.querySelector('svg')).toBeTruthy(); // GraphCanvas rendered
    unmount(c);
  });

  it('shows direction/depth controls only when showControls is set', () => {
    const c1 = mount(GraphModal, { target, props: { open: true, graphData, focusId: 'root', showControls: false } });
    expect(document.querySelector('.depth-ctrl')).toBeFalsy();
    unmount(c1);
    const c2 = mount(GraphModal, { target, props: { open: true, graphData, focusId: 'root', showControls: true } });
    expect(document.querySelector('.depth-ctrl')).toBeTruthy();
    unmount(c2);
  });

  it('calls onClose when the close button is clicked', () => {
    let closed = false;
    const c = mount(GraphModal, { target, props: { open: true, graphData, focusId: 'root', onClose: () => { closed = true; } } });
    (document.querySelector('.graph-modal-close') as HTMLButtonElement).click();
    expect(closed).toBe(true);
    unmount(c);
  });
});
