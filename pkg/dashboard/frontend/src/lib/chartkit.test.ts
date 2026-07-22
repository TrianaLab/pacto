import { describe, it, expect, vi } from 'vitest';
import { resolvePalette, prefersReducedMotion, emptyState, sharedTooltip } from './chartkit';

describe('chartkit', () => {
  it('resolvePalette returns token values with fallbacks', () => {
    const el = document.createElement('div');
    el.style.setProperty('--c-ok', '#34d399');
    el.style.setProperty('--c-text', '#f1f5f9');
    document.body.appendChild(el);
    const pal = resolvePalette(el);
    expect(pal.ok).toBeTruthy();
    expect(pal.text).toBeTruthy();
    document.body.removeChild(el);
  });
  it('emptyState renders a message box', () => {
    const el = document.createElement('div');
    emptyState(el, 'No data');
    expect(el.textContent).toContain('No data');
    expect(el.querySelector('.state-box')).not.toBeNull();
  });
  it('prefersReducedMotion respects matchMedia', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q }));
    expect(prefersReducedMotion()).toBe(true);
    vi.unstubAllGlobals();
  });
  it('sharedTooltip attaches and shows without throwing', () => {
    const el = document.createElement('div');
    const t = sharedTooltip(); t.attach(el); t.show('hi', 10, 10); t.hide();
    expect(el.querySelector('div')).not.toBeNull();
  });
});
