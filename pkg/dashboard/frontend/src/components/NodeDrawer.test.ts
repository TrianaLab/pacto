/**
 * Component render tests for NodeDrawer.svelte.
 * Verifies it renders service details and that close button + Escape call onClose.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import NodeDrawer from './NodeDrawer.svelte';

const sampleDrawerData = {
  name: 'payments',
  version: '1.0.0',
  status: 'Compliant',
  blastRadius: 2,
  owner: { team: 'core', dri: 'ada' },
  sources: ['k8s', 'oci'],
  external: false,
  dependencies: [
    { name: 'ledger', required: true, type: 'dependency' },
    { name: 'notifier', required: false, type: 'reference' },
  ],
  dependents: ['checkout', 'api-gateway'],
};

describe('NodeDrawer — render and close behavior', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  it('renders when data is provided', () => {
    const component = mount(NodeDrawer, {
      target,
      props: { data: sampleDrawerData, onClose: () => {} },
    });

    const drawer = target.querySelector('.drawer');
    expect(drawer).toBeTruthy();
    expect(target.textContent).toContain('payments');

    unmount(component);
  });

  it('does not render when data is null', () => {
    const component = mount(NodeDrawer, {
      target,
      props: { data: null, onClose: () => {} },
    });

    const drawer = target.querySelector('.drawer');
    expect(drawer).toBeNull();

    unmount(component);
  });

  it('displays service name, status and metadata', () => {
    const component = mount(NodeDrawer, {
      target,
      props: { data: sampleDrawerData, onClose: () => {} },
    });

    const text = target.textContent || '';
    expect(text).toContain('payments');
    expect(text).toContain('1.0.0'); // version
    expect(text).toContain('core'); // owner team

    unmount(component);
  });

  it('lists dependencies with their types', () => {
    const component = mount(NodeDrawer, {
      target,
      props: { data: sampleDrawerData, onClose: () => {} },
    });

    const text = target.textContent || '';
    expect(text).toContain('ledger');
    expect(text).toContain('notifier');
    // "req" badge for required deps, "ref" badge for references
    expect(text).toContain('req');
    expect(text).toContain('ref');

    unmount(component);
  });

  it('lists dependents', () => {
    const component = mount(NodeDrawer, {
      target,
      props: { data: sampleDrawerData, onClose: () => {} },
    });

    const text = target.textContent || '';
    expect(text).toContain('checkout');
    expect(text).toContain('api-gateway');

    unmount(component);
  });

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn();
    const component = mount(NodeDrawer, {
      target,
      props: { data: sampleDrawerData, onClose },
    });

    const closeBtn = target.querySelector('.drawer-close') as HTMLButtonElement;
    expect(closeBtn).toBeTruthy();
    closeBtn.click();

    expect(onClose).toHaveBeenCalled();

    unmount(component);
  });

  it('calls onClose when Escape is pressed', () => {
    const onClose = vi.fn();
    const component = mount(NodeDrawer, {
      target,
      props: { data: sampleDrawerData, onClose },
    });

    // Dispatch Escape keydown on the window (the component listens via svelte:window).
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));

    expect(onClose).toHaveBeenCalled();

    unmount(component);
  });
});
