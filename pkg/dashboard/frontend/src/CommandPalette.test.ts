/**
 * Component render tests for CommandPalette.svelte.
 * Verifies keyboard nav (Arrow/Enter/Escape) and the focus trap.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import CommandPalette from './CommandPalette.svelte';

const sampleServices = [
  { name: 'payments', contractStatus: 'Compliant' },
  { name: 'ledger', contractStatus: 'Warning' },
];

describe('CommandPalette — keyboard nav and focus trap', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  it('renders when open=true with input and results', () => {
    const component = mount(CommandPalette, {
      target,
      props: { open: true, services: sampleServices, onClose: () => {}, onAction: () => {} },
    });

    expect(target.querySelector('.cp-panel')).toBeTruthy();
    const input = target.querySelector('.cp-input-row input');
    expect(input).toBeTruthy();
    expect(target.querySelector('.cp-results')).toBeTruthy();

    unmount(component);
  });

  it('focuses the input when opened', async () => {
    const component = mount(CommandPalette, {
      target,
      props: { open: true, services: sampleServices, onClose: () => {}, onAction: () => {} },
    });

    await new Promise(resolve => setTimeout(resolve, 10)); // wait for queueMicrotask focus
    const input = target.querySelector('.cp-input-row input') as HTMLInputElement;
    expect(document.activeElement).toBe(input);

    unmount(component);
  });

  it('navigates results with ArrowDown/ArrowUp', async () => {
    const component = mount(CommandPalette, {
      target,
      props: { open: true, services: sampleServices, onClose: () => {}, onAction: () => {} },
    });

    await new Promise(resolve => setTimeout(resolve, 10)); // wait for focus

    const input = target.querySelector('.cp-input-row input') as HTMLInputElement;

    // First result should be selected initially (selectedIdx=0).
    let items = target.querySelectorAll('.cp-item');
    expect(items.length).toBeGreaterThan(0);
    expect(items[0].classList.contains('selected')).toBe(true);

    // Press ArrowDown to move to the next result.
    const keydownDown = new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true });
    input.dispatchEvent(keydownDown);
    await new Promise(resolve => setTimeout(resolve, 0));

    items = target.querySelectorAll('.cp-item');
    expect(items.length).toBeGreaterThan(1);
    expect(items[1].classList.contains('selected')).toBe(true);

    // Press ArrowUp to move back.
    const keydownUp = new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true });
    input.dispatchEvent(keydownUp);
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(items[0].classList.contains('selected')).toBe(true);

    unmount(component);
  });

  it('calls onClose when Escape is pressed', async () => {
    const onClose = vi.fn();
    const component = mount(CommandPalette, {
      target,
      props: { open: true, services: sampleServices, onClose, onAction: () => {} },
    });

    const input = target.querySelector('.cp-input-row input') as HTMLInputElement;
    input.focus();
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(onClose).toHaveBeenCalled();

    unmount(component);
  });

  it('activates a command when Enter is pressed', async () => {
    const onAction = vi.fn();
    const component = mount(CommandPalette, {
      target,
      props: { open: true, services: sampleServices, onClose: () => {}, onAction },
    });

    const input = target.querySelector('.cp-input-row input') as HTMLInputElement;
    input.focus();

    // Press Enter to activate the selected command (first result).
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await new Promise(resolve => setTimeout(resolve, 0));

    // The first command is typically a service or navigation action; either onAction
    // is called or location.hash changes (for href commands). Check for either.
    const hashChanged = location.hash !== '';
    expect(onAction.mock.calls.length > 0 || hashChanged).toBe(true);

    unmount(component);
  });

  it('traps focus with Tab/Shift+Tab within the panel', async () => {
    const component = mount(CommandPalette, {
      target,
      props: { open: true, services: sampleServices, onClose: () => {}, onAction: () => {} },
    });

    await new Promise(resolve => setTimeout(resolve, 10)); // wait for focus
    const input = target.querySelector('.cp-input-row input') as HTMLInputElement;
    expect(document.activeElement).toBe(input);

    const panel = target.querySelector('.cp-panel') as HTMLElement;
    const buttons = target.querySelectorAll('.cp-item');
    expect(buttons.length).toBeGreaterThan(0);

    // Dispatch Tab on the input to trigger the trap logic.
    const tabEvent = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    input.dispatchEvent(tabEvent);
    await new Promise(resolve => setTimeout(resolve, 0));

    // The trap should have cycled to a button.
    const focusedAfterTab = document.activeElement;
    expect(panel.contains(focusedAfterTab)).toBe(true);
    expect(focusedAfterTab).not.toBe(input);

    // Shift+Tab should cycle backward within the panel.
    const shiftTabEvent = new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true });
    (document.activeElement as HTMLElement).dispatchEvent(shiftTabEvent);
    await new Promise(resolve => setTimeout(resolve, 0));

    // Focus should still be within the panel.
    expect(panel.contains(document.activeElement)).toBe(true);

    unmount(component);
  });
});
