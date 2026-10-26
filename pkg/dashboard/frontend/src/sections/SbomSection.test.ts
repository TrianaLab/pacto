import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import SbomSection from './SbomSection.svelte';

let target: HTMLElement;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { target.remove(); });

describe('SbomSection', () => {
  it('renders package rows with format', () => {
    const c = mount(SbomSection, { target, props: {
      open: true,
      sbom: { format: 'spdx', packages: [{ name: 'libfoo', version: '1.2.3', license: 'MIT' }] },
    }});
    expect(target.textContent).toContain('libfoo');
    expect(target.textContent).toContain('1.2.3');
    expect(target.textContent).toContain('spdx');
    unmount(c);
  });

  it('says so when sbom is null, rather than disappearing', () => {
    const c = mount(SbomSection, { target, props: { open: true, sbom: null } });
    expect(target.querySelector('.section')).not.toBeNull();
    expect(target.textContent).toContain('SBOM');
    expect(target.textContent).toContain('None declared');
    unmount(c);
  });

  it('shows fifty packages and offers the other 2,950', () => {
    const packages = Array.from({ length: 3000 }, (_, i) => ({ name: `pkg-${i}`, version: '1.0.0' }));
    const c = mount(SbomSection, { target, props: { open: true, sbom: { format: 'spdx', packages } } });
    expect(target.querySelectorAll('tbody tr').length).toBe(50);
    const more = target.querySelector('[data-testid="sbom-show-all"]') as HTMLButtonElement;
    expect(more.textContent?.trim()).toBe('Show all 3,000 packages');
    more.click();
    flushSync();
    expect(target.querySelectorAll('tbody tr').length).toBe(3000);
    unmount(c);
  });
});
