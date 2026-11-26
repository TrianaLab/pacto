import { describe, it, expect } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ListingNote from './ListingNote.svelte';

function render(listing: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ListingNote, { target, props: { listing } });
  flushSync();
  const text = target.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  unmount(c); target.remove();
  return text;
}

describe('ListingNote per cluster (514-S7)', () => {
  it('says in the partial line what each other cluster could not read and counts the hidden ones', () => {
    const t = render({ completeness: 'partial', hiddenClusters: 1, clusters: [
      { id: 'uid-eu', name: 'eu-prod', completeness: 'complete' },
      { id: 'uid-us', name: 'us-prod', completeness: 'partial', why: 'the review failed or took longer than 5s, so its delivery objects are hidden' },
      { id: 'uid-ap', name: 'ap-dev', completeness: 'partial', hidden: 2 },
    ] });
    expect(t).toBe('Showing 2 of 4 clusters: us-prod: not read, ap-dev: 2 namespaces hidden; 1 cluster hidden.');
    expect(t).not.toContain('uid-');
  });
  it('counts hidden clusters alone', () => {
    expect(render({ completeness: 'partial', hiddenClusters: 2, clusters: [{ id: 'uid-eu', name: 'eu-prod', completeness: 'complete' }] }))
      .toBe('Showing 1 of 3 clusters: 2 clusters hidden.');
  });
  it('leaves the home cluster to the namespace sentence', () => {
    const t = render({ completeness: 'partial', unreadable: ['team-a'], clusters: [
      { id: 'uid-eu', name: 'eu-prod', completeness: 'partial', unreadable: ['team-a'] },
      { id: 'uid-us', name: 'us-prod', completeness: 'complete' },
    ] });
    expect(t).toBe('You may not read the namespace team-a, so this may be incomplete.');
  });
  it('adds nothing with one cluster', () => {
    expect(render({ completeness: 'partial', unreadable: ['team-a'] })).toBe('You may not read the namespace team-a, so this may be incomplete.');
  });
});
