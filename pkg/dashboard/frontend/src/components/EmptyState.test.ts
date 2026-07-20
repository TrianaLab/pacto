/**
 * Render tests for EmptyState.svelte — focus on the error/retry variant that
 * keeps a failed fetch from masquerading as a benign empty state.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import EmptyState from './EmptyState.svelte';

describe('EmptyState', () => {
  let target: HTMLElement;
  beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
  afterEach(() => { document.body.removeChild(target); });

  it('renders a skeleton while loading', () => {
    const c = mount(EmptyState, { target, props: { loading: true } });
    expect(target.querySelector('.skeleton')).toBeTruthy();
    unmount(c);
  });

  it('shows the error text and a Retry button in the error variant', () => {
    const c = mount(EmptyState, { target, props: { error: 'Backend unreachable', onRetry: () => {} } });
    expect(target.querySelector('.is-error')).toBeTruthy();
    expect(target.textContent).toContain('Backend unreachable');
    expect(target.querySelector('.retry-btn')).toBeTruthy();
    unmount(c);
  });

  it('calls onRetry when the button is clicked', () => {
    let clicked = 0;
    const c = mount(EmptyState, { target, props: { error: true, onRetry: () => { clicked++; } } });
    (target.querySelector('.retry-btn') as HTMLButtonElement).click();
    expect(clicked).toBe(1);
    unmount(c);
  });

  it('omits the Retry button when no onRetry is given', () => {
    const c = mount(EmptyState, { target, props: { error: 'boom' } });
    expect(target.querySelector('.retry-btn')).toBeFalsy();
    unmount(c);
  });

  it('renders a plain empty state (no error styling) for title/message only', () => {
    const c = mount(EmptyState, { target, props: { title: 'No services', message: 'Nothing here' } });
    expect(target.querySelector('.is-error')).toBeFalsy();
    expect(target.querySelector('.retry-btn')).toBeFalsy();
    expect(target.textContent).toContain('No services');
    unmount(c);
  });
});
