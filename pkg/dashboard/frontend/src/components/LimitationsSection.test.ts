import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import LimitationsSection from './LimitationsSection.svelte';

describe('LimitationsSection', () => {
  let target: HTMLElement;
  beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
  afterEach(() => { document.body.removeChild(target); });

  it('renders nothing for an empty or missing page', () => {
    for (const page of [undefined, { total: 0, count: 0, truncated: false, items: [] }]) {
      const c = mount(LimitationsSection, { target, props: { page } });
      expect(target.textContent?.trim()).toBe('');
      unmount(c);
    }
  });

  it('folds the limitations under one titled section', () => {
    const page = { total: 3, count: 1, truncated: true, items: [{ code: 'no-observation', message: 'Nothing observed it' }] };
    const c = mount(LimitationsSection, { target, props: { page } });
    expect(target.textContent).toContain('Limitations');
    expect(target.textContent).toContain('What Pacto could not determine');
    unmount(c);
  });
});
