/**
 * LazyView renders a route view loaded on first use, with its props, and says
 * so when the module cannot be loaded instead of leaving a blank page.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import LazyView from './LazyView.svelte';
// @ts-expect-error — Svelte component has no declaration file
import EmptyState from './EmptyState.svelte';

describe('LazyView', () => {
  let target: HTMLElement;
  beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
  afterEach(() => { document.body.removeChild(target); });

  it('mounts the loaded view with the props it was given', async () => {
    const c = mount(LazyView, { target, props: { load: () => Promise.resolve({ default: EmptyState }), loading: true } });
    await vi.waitFor(() => expect(target.querySelector('.skeleton')).toBeTruthy());
    unmount(c);
  });

  it('says the view could not be loaded', async () => {
    const c = mount(LazyView, { target, props: { load: () => Promise.reject(new Error('chunk gone')) } });
    await vi.waitFor(() => expect(target.querySelector('[role="alert"]')?.textContent).toContain('chunk gone'));
    unmount(c);
  });
});
