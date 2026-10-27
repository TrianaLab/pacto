/**
 * HelpTip must open, and stay open, on every way a reader can ask for it: a mouse click
 * (hover then focus then click), a touch tap (focus then click) and Tab then Enter.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import HelpTip from './HelpTip.svelte';

describe('HelpTip', () => {
  let target: HTMLElement;
  let c: ReturnType<typeof mount>;
  const btn = () => target.querySelector('button') as HTMLButtonElement;
  const body = () => target.querySelector('.help-body') as HTMLElement;
  beforeEach(() => {
    target = document.createElement('div'); document.body.appendChild(target);
    c = mount(HelpTip, { target, props: { label: 'the dots', text: 'One per environment.' } });
  });
  afterEach(() => { unmount(c); document.body.removeChild(target); });

  it('is named About …', () => {
    expect(btn().getAttribute('aria-label')).toBe('About the dots');
  });

  it('stays open after a mouse click', () => {
    target.querySelector('.help')!.dispatchEvent(new MouseEvent('mouseenter'));
    btn().focus(); btn().click(); flushSync();
    expect(body().hidden).toBe(false);
  });

  it('opens on a touch tap', () => {
    btn().focus(); btn().click(); flushSync();
    expect(body().hidden).toBe(false);
  });

  it('stays open on Tab then Enter, and Escape closes it', () => {
    btn().focus(); flushSync();
    expect(body().hidden).toBe(false);
    btn().click(); flushSync(); // Enter on a button fires click
    expect(body().hidden).toBe(false);
    btn().dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); flushSync();
    expect(body().hidden).toBe(true);
  });
});
