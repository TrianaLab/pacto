/**
 * The Cluster fact of a target page: which cluster reported the target, how its name
 * was found (or who reported it), and the cluster UID behind the name.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';

// @ts-expect-error — Svelte component has no declaration file
import TargetEntity from './TargetEntity.svelte';

const UID = '5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11';

function render(target: Record<string, unknown>) {
  const el = document.createElement('div');
  document.body.appendChild(el);
  const component = mount(TargetEntity, { target: el, props: { detail: { target: { linkState: 'unresolved', identity: {}, ...target } } } });
  return { el, component };
}

let mounted: ReturnType<typeof render> | null = null;
afterEach(() => { if (mounted) unmount(mounted.component); mounted = null; document.body.innerHTML = ''; });

const fact = (el: HTMLElement) => el.querySelector('[data-testid="target-cluster"]') as HTMLElement | null;
const text = (el: HTMLElement | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();

describe('TargetEntity cluster fact', () => {
  it.each([
    ['node-label', 'node label'],
    ['context', 'context name'],
    ['file', 'target-state file'],
    ['uid', 'cluster id'],
  ])('names how a %s name was found', (nameSource, words) => {
    mounted = render({ cluster: { id: UID, name: 'production', nameSource }, clusterRelation: 'runs-in' });
    const f = fact(mounted.el);
    expect(text(f)).toBe(`Runs in cluster production (from ${words}) uid 5b0c6c3e`);
    expect(f?.querySelector('bdi')?.textContent).toBe('production');
    expect(f?.querySelector(`[title="${UID}"]`)?.textContent).toBe('5b0c6c3e');
  });

  it('says the cluster only reported the target unless a Kubernetes source read it there', () => {
    mounted = render({ cluster: { id: UID, name: 'production', nameSource: 'context' } });
    expect(text(fact(mounted.el))).toMatch(/^Reported through cluster production/);
    unmount(mounted.component);
    mounted = render({ cluster: { id: UID, name: 'production', nameSource: 'context' }, clusterRelation: 'reported-through' });
    expect(text(fact(mounted.el))).toMatch(/^Reported through cluster production/);
  });

  it('says who reported a name the process did not read itself', () => {
    mounted = render({ cluster: { id: UID, name: 'eu-prod', nameSource: 'node-label', reportedBy: 'evidence-x' }, clusterRelation: 'reported-through' });
    expect(text(fact(mounted.el))).toBe('Reported through cluster eu-prod (reported by evidence-x as node label) uid 5b0c6c3e');
  });

  it('shows no cluster row when no source reported a cluster', () => {
    mounted = render({});
    expect(fact(mounted.el)).toBeNull();
    expect(mounted.el.textContent).not.toContain('cluster');
  });
});
