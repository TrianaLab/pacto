/**
 * The Cluster filter on the Services and Targets lists: a select over meta.clusters
 * and one caption line, shown only with two or more clusters.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ClusterFilter from './ClusterFilter.svelte';

const long = 'eu-prod-' + 'x'.repeat(55);
const clusters = [
  { id: 'uid-eu', name: long, status: 'available', targets: 3 },
  { id: 'uid-us', name: 'us-prod', status: 'available', access: 'no-access', targets: 0 },
];

function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ClusterFilter, { target, props: { value: '', onchange: () => {}, ...props } });
  flushSync();
  return { target, done: () => { unmount(c); target.remove(); } };
}

describe('ClusterFilter', () => {
  it('renders nothing with fewer than two clusters', () => {
    for (const cs of [undefined, [], [clusters[0]]]) {
      const { target, done } = render({ clusters: cs });
      expect(target.querySelector('select')).toBeNull();
      expect(target.textContent?.trim()).toBe('');
      done();
    }
  });

  it('lists every cluster and reports the chosen UID', () => {
    const onchange = vi.fn();
    const { target, done } = render({ clusters, onchange });
    const select = target.querySelector('select') as HTMLSelectElement;
    expect(Array.from(select.options).map((o) => o.value)).toEqual(['', 'uid-eu', 'uid-us']);
    expect(select.options[2].textContent).toBe('us-prod (no access)');
    select.value = 'uid-eu';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    expect(onchange).toHaveBeenCalledWith('uid-eu');
    done();
  });

  it('captions the hidden cluster with its reason, names isolated in bdi', () => {
    const { target, done } = render({ clusters, value: 'uid-eu' });
    const caption = target.querySelector('[data-testid="cluster-caption"]');
    expect(caption?.textContent?.replace(/\s+/g, ' ').trim()).toBe('Showing 1 of 2 clusters (us-prod: no access)');
    expect(caption?.querySelector('bdi')?.getAttribute('title')).toBe('us-prod');
    done();
  });
});
