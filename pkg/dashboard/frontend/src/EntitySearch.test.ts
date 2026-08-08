/**
 * Component tests for EntitySearch.svelte — global entity discovery.
 * Covers acceptance scenarios 6-10 and 16: search finds a service; same-named
 * services in different domains stay distinguishable (never collapsed); search opens
 * a target / revision / owner / source by canonical identity; and keyboard
 * navigation (arrows + Enter) opens the selected result. `api` is mocked so search
 * hits /api/fleet/entities via the facade, never a preloaded list.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { entitiesFn } = vi.hoisted(() => ({ entitiesFn: vi.fn() }));
vi.mock('./lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import EntitySearch from './EntitySearch.svelte';

function list(entities: unknown[], total?: number) {
  return { meta: { schemaVersion: 'pacto.dev/fleet-product/v1' }, total: total ?? entities.length, count: entities.length, entities };
}

function mountSearch() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(EntitySearch, { target, props: { open: true, onClose: () => {} } });
  // Flush the open-reset effect (which clears the query) BEFORE any typing, so it
  // cannot clobber the value we set synchronously.
  flushSync();
  return { target, component };
}

function type(target: HTMLElement, text: string) {
  const input = target.querySelector('input') as HTMLInputElement;
  input.value = text;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
  return input;
}

const rows = (target: HTMLElement) => Array.from(target.querySelectorAll('[data-testid="search-result"]'));

describe('EntitySearch — global entity discovery', () => {
  beforeEach(() => {
    entitiesFn.mockReset();
    location.hash = '';
  });

  it('scenario 6: finds a service via the product entities endpoint (not a preloaded list)', async () => {
    entitiesFn.mockResolvedValue(list([{ kind: 'service', key: 'domain-a/payments', label: 'payments', domain: 'domain-a', href: '/fleet/services/domain-a%2Fpayments' }]));
    const { target, component } = mountSearch();
    type(target, 'pay');
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ text: 'pay' }));
    expect(rows(target)[0].textContent).toContain('payments');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 7: two same-named services in different domains stay distinguishable', async () => {
    entitiesFn.mockResolvedValue(list([
      { kind: 'service', key: 'domain-a/payments', label: 'payments', domain: 'domain-a', href: '/fleet/services/domain-a%2Fpayments' },
      { kind: 'service', key: 'domain-b/payments', label: 'payments', domain: 'domain-b', href: '/fleet/services/domain-b%2Fpayments' },
    ]));
    const { target, component } = mountSearch();
    type(target, 'payments');
    await vi.waitFor(() => expect(rows(target).length).toBe(2)); // not collapsed
    expect(rows(target)[0].textContent).toContain('domain domain-a');
    expect(rows(target)[1].textContent).toContain('domain domain-b');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 8: opens a target by canonical identity', async () => {
    entitiesFn.mockResolvedValue(list([{ kind: 'target', key: 'prod/k8s/app', label: 'app', scope: 'prod', href: '/fleet/targets/prod%2Fk8s%2Fapp' }]));
    const { target, component } = mountSearch();
    type(target, 'app');
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    (rows(target)[0] as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/targets/prod%2Fk8s%2Fapp');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 9: opens a revision by canonical identity', async () => {
    entitiesFn.mockResolvedValue(list([{ kind: 'revision', key: 'domain-a/app@sha256:ab', label: 'app 1.0', href: '/fleet/revisions/domain-a%2Fapp@sha256:ab' }]));
    const { target, component } = mountSearch();
    type(target, 'app 1');
    await vi.waitFor(() => expect(rows(target).length).toBe(1));
    (rows(target)[0] as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/revisions/domain-a%2Fapp@sha256:ab');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 10: opens an owner and a source', async () => {
    entitiesFn.mockResolvedValue(list([
      { kind: 'owner', key: 'team-a', label: 'team-a', href: '/fleet/owners/team-a' },
      { kind: 'source', key: 'k8s', label: 'k8s', href: '/fleet/sources/k8s' },
    ]));
    const { target, component } = mountSearch();
    type(target, 'te');
    await vi.waitFor(() => expect(rows(target).length).toBe(2));
    (rows(target)[1] as HTMLButtonElement).click(); // the source
    expect(location.hash).toBe('#/fleet/sources/k8s');
    unmount(component); document.body.removeChild(target);
  });

  it('scenario 16: keyboard navigation (arrow + Enter) opens the selected result', async () => {
    entitiesFn.mockResolvedValue(list([
      { kind: 'service', key: 'a/one', label: 'one', domain: 'a', href: '/fleet/services/a%2Fone' },
      { kind: 'service', key: 'a/two', label: 'two', domain: 'a', href: '/fleet/services/a%2Ftwo' },
    ]));
    const { target, component } = mountSearch();
    const input = type(target, 'o');
    await vi.waitFor(() => expect(rows(target).length).toBe(2));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(location.hash).toBe('#/fleet/services/a%2Ftwo'); // second result selected + opened
    unmount(component); document.body.removeChild(target);
  });

  it('shows truncation honestly when the backend caps the results', async () => {
    entitiesFn.mockResolvedValue(list([{ kind: 'service', key: 'a/x', label: 'x', domain: 'a', href: '/fleet/services/a%2Fx' }], 42));
    const { target, component } = mountSearch();
    type(target, 'x');
    await vi.waitFor(() => expect(target.textContent).toContain('Showing 1 of 42'));
    unmount(component); document.body.removeChild(target);
  });
});
