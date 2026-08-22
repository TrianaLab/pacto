/**
 * DiffView is the LEGACY, name+version-keyed compare screen. App mounts it only on a
 * non-Fleet host (the offline `pacto doc` export); a Fleet host canonicalizes #/diff to
 * the Change analysis workspace, where comparing revisions is RevisionKey-based end to
 * end. So the cross-link this screen used to grow into the product impact workspace is
 * gone, and with it the name->ServiceKey resolution that guarded it (that resolution now
 * lives in ChangeAnalysisView, where it is tested). What must hold here is that the
 * legacy screen stays legacy: no product routes, no Product API traffic. `api` is mocked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { versionsFn, diffFn, capsFn, entitiesFn } = vi.hoisted(() => ({
  versionsFn: vi.fn(), diffFn: vi.fn(), capsFn: vi.fn(), entitiesFn: vi.fn(),
}));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual,
    api: {
      versions: (...a: unknown[]) => versionsFn(...a),
      diff: (...a: unknown[]) => diffFn(...a),
      capabilities: (...a: unknown[]) => capsFn(...a),
      fleetEntities: (...a: unknown[]) => entitiesFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import DiffView from './DiffView.svelte';

function mountView() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(DiffView, { target, props: { name: 'payments', services: [{ name: 'payments' }] } });
  return { target, component };
}
const productLinks = (t: HTMLElement) =>
  Array.from(t.querySelectorAll('a')).filter((a) => (a.getAttribute('href') || '').includes('/fleet/'));

describe('DiffView — the legacy compare screen carries no product plumbing', () => {
  beforeEach(() => {
    for (const f of [versionsFn, diffFn, capsFn, entitiesFn]) f.mockReset();
    versionsFn.mockResolvedValue([]);
    capsFn.mockResolvedValue({ fleet: true });
  });

  it('never links into a product route from a service NAME', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => expect(versionsFn).toHaveBeenCalled());
    await Promise.resolve();
    expect(productLinks(target)).toEqual([]);
    unmount(component); document.body.removeChild(target);
  });

  it('makes no Product API call — it is the only UI on a host that has no Product API', async () => {
    const { target, component } = mountView();
    await vi.waitFor(() => expect(versionsFn).toHaveBeenCalled());
    await Promise.resolve();
    expect(entitiesFn).not.toHaveBeenCalled();
    expect(capsFn).not.toHaveBeenCalled();
    unmount(component); document.body.removeChild(target);
  });
});
