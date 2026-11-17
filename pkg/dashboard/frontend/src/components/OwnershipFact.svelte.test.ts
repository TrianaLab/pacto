/**
 * The owner value's provenance mark: absent for a declared owner, a discreet
 * help mark with the sentence in its tooltip for every other provenance.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import OwnershipFact from './OwnershipFact.svelte';
import { ownerProvenanceText } from '../lib/entityLabels.ts';

const owner = { kind: 'owner', key: 'team:pay', label: 'pay', href: '/fleet/owners/team%3Apay' };

function render(provenance?: string) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(OwnershipFact, { target, props: { ownership: { declared: true, owner: 'pay', ref: owner, provenance } } });
  flushSync();
  return { target, done: () => { unmount(c); target.remove(); } };
}

describe('OwnershipFact owner provenance', () => {
  it.each([undefined, 'declared'])('shows no mark for %s', (p) => {
    const { target, done } = render(p);
    expect(target.querySelector('[data-testid="owner-provenance"]')).toBeNull();
    expect(target.querySelector('a')?.getAttribute('href')).toContain('team%3Apay');
    done();
  });

  it.each(['declared-via-target', 'attested', 'inferred'])('marks %s with its sentence', (p) => {
    const { target, done } = render(p);
    const mark = target.querySelector('[data-testid="owner-provenance"]');
    expect(mark).toBeTruthy();
    expect(mark?.querySelector('button')?.getAttribute('aria-label')).toBe('About owner provenance');
    expect(mark?.querySelector('button')?.textContent).toBe('i'); // the shared ProvenanceMark, not a help tip
    expect(mark?.querySelector('[role="note"]')?.textContent).toBe(ownerProvenanceText(p));
    expect(target.querySelector('a')?.getAttribute('href')).toContain('team%3Apay');
    expect(target.querySelector('a')?.textContent).toContain('pay');
    done();
  });
});
