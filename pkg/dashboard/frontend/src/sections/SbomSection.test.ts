import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
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

  it('renders nothing when sbom is null', () => {
    const c = mount(SbomSection, { target, props: { open: true, sbom: null } });
    expect(target.querySelector('.section')).toBeNull();
    unmount(c);
  });
});
