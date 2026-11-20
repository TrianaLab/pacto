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

function render(provenance?: string, source?: string, declaredIn: unknown = null) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(OwnershipFact, { target, props: { declaredIn, ownership: { declared: true, owner: 'pay', ref: owner, provenance, source } } });
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

  it('puts the whole attested source in the one mark, adding no element', () => {
    const source = 'CODEOWNERS:7 at a1b2c3d, signed by ' + 'github.com/acme/invoices/.github/workflows/'.padEnd(160, 'x') + '/ci.yml';
    expect(source.length).toBeGreaterThanOrEqual(200);
    const declared = render('declared');
    const base = declared.target.querySelectorAll('*').length;
    declared.done();
    const plain = render('attested');
    const withMark = plain.target.querySelectorAll('*').length;
    plain.done();
    const { target, done } = render('attested', source);
    const marks = target.querySelectorAll('[data-testid="owner-provenance"]');
    expect(marks).toHaveLength(1);
    const tip = marks[0].querySelector('[role="note"]')?.textContent ?? '';
    expect(tip).toBe(ownerProvenanceText('attested') + '. Source: ' + source + '.');
    expect(tip).toContain('never clears a gate. Source: ');
    expect(tip).toContain('never clears a gate');
    expect(target.querySelectorAll('*').length).toBe(withMark);
    expect(withMark).toBeGreaterThan(base);
    done();
  });

  it('says where a declared owner was declared, never for an attested one', () => {
    const rev = { kind: 'revision', key: 'svc@1.0.0', label: 'svc@1.0.0', href: '/fleet/revisions/svc%401.0.0' };
    const declared = render('declared', undefined, rev);
    expect(declared.target.querySelector('[data-testid="owner-declared-in"]')?.textContent).toContain('Declared in');
    declared.done();
    const attested = render('attested', 'catalog-info.yaml:7', rev);
    expect(attested.target.querySelector('[data-testid="owner-declared-in"]')).toBeNull();
    attested.done();
  });
});
