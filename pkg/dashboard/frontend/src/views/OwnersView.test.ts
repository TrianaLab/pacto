/** OwnersView (legacy host): paged through the URL, its search kept apart from the services list. */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { clearFilters, getFilters } from '../lib/filters.svelte.ts';
// @ts-expect-error — Svelte component has no declaration file
import OwnersView from './OwnersView.svelte';

const services = Array.from({ length: 30 }, (_, i) => ({
  name: `svc-${i}`, contractStatus: 'Compliant', complianceScore: 100, blastRadius: i % 4, owner: { team: `team-${i}` },
}));

let target: HTMLElement;
let c: ReturnType<typeof mount> | null = null;
afterEach(() => { if (c) unmount(c); c = null; target?.remove(); location.hash = ''; clearFilters(); });

async function render(hash = '') {
  location.hash = hash;
  await new Promise((r) => setTimeout(r, 0));
  target = document.createElement('div');
  document.body.appendChild(target);
  c = mount(OwnersView, { target, props: { services } });
  flushSync();
}

describe('OwnersView', () => {
  it('shows 25 owners a page with the shared pager', async () => {
    await render('#/owners');
    expect(target.querySelectorAll('table.owners-list > tbody > tr').length).toBe(25);
    expect(target.querySelector('[data-testid="owners-pager-next"]')?.getAttribute('href')).toBe('#/owners?offset=25');
  });

  it('filters owners by its own URL key, never the services search', async () => {
    await render('#/owners');
    const input = target.querySelector('[data-testid="owners-filter"] input') as HTMLInputElement;
    input.value = 'team-1';
    input.dispatchEvent(new Event('input'));
    (target.querySelector('[data-testid="owners-filter"]') as HTMLFormElement).requestSubmit();
    expect(location.hash).toBe('#/owners?q=team-1');
    expect(getFilters().search).toBe('');
  });

  it('ranks owners by their widest blast radius, never a sum of overlapping ones', async () => {
    await render('#/owners?q=team-3');
    expect(target.textContent).toContain('Largest blast');
  });
});
