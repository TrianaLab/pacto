import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import CapabilitiesSection from './CapabilitiesSection.svelte';

let target: HTMLElement;

beforeEach(() => {
  target = document.createElement('div');
  document.body.appendChild(target);
});

afterEach(() => target.remove());

describe('CapabilitiesSection', () => {
  it('renders tools with method, path and a write badge for mutating ops', () => {
    const c = mount(CapabilitiesSection, {
      target,
      props: {
        open: true,
        capabilities: [
          { name: 'getUser', method: 'GET', path: '/users/{id}', summary: 'Get a user', mutating: false },
          { name: 'createRefund', method: 'POST', path: '/refunds', summary: 'Create refund', mutating: true },
        ],
        skills: [],
      },
    });
    const text = target.textContent || '';
    expect(text).toContain('/users/{id}');
    expect(text).toContain('createRefund');
    expect(text).toContain('write'); // mutating badge
    expect(target.querySelectorAll('.cap-table tbody tr').length).toBe(2);
    unmount(c);
  });

  it('lists skills and renders markdown content when expanded', async () => {
    const c = mount(CapabilitiesSection, {
      target,
      props: {
        open: true,
        capabilities: [],
        skills: [{ name: 'refund.md', content: '# Refund flow' }],
      },
    });
    expect(target.textContent).toContain('refund.md');
    // collapsed by default → content hidden
    expect(target.querySelector('.markdown-body')).toBeNull();
    const btn = target.querySelector('.detail-card-header') as HTMLButtonElement;
    btn.click();
    await Promise.resolve();
    // rendered as markdown (heading element), not raw text in a <pre>
    const md = target.querySelector('.markdown-body');
    expect(md).not.toBeNull();
    expect(md?.querySelector('h1')?.textContent).toContain('Refund flow');
    unmount(c);
  });

  it('renders nothing when there are no capabilities or skills', () => {
    const c = mount(CapabilitiesSection, { target, props: { open: true, capabilities: [], skills: [] } });
    expect(target.querySelector('.section')).toBeNull();
    expect(target.textContent?.trim()).toBe('');
    unmount(c);
  });
});
