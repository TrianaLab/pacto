/**
 * Where the values of a declared service come from (513-S5): one closed fold, one
 * discreet mark per row, inferred rows muted and filtered by one checkbox, honest about
 * truncation and about what the viewer cannot see, and one facts block for the newest
 * replaced workload only.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import FieldProvenanceFold from './FieldProvenanceFold.svelte';
import { fieldProvenanceText } from '../lib/fieldProvenance.ts';

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

const asOf = '2026-11-17T12:00:00Z';
const LONG = `ghcr.io/acme/${'x'.repeat(200)}@sha256:0123456789abcdef`;
const rows = [
  { field: 'service.owner', value: 'billing', provenance: 'declared', source: 'pacto.yaml of invoices@0.1.0', asOf },
  { field: 'dependencies[ledger].compatibility', value: '^2.3.0', provenance: 'accepted', by: 'ana@acme.example', source: 'pacto.yaml of invoices@0.1.0', asOf },
  { field: 'dependencies[audit].compatibility', value: 'unknown', provenance: 'declared', proposed: '^1.4.0', source: 'cluster:runtime k8s:billing/Deployment/invoices', completeness: 'partial', asOf },
  { field: 'service.repository', value: 'acme/invoices', provenance: 'attested', source: 'CODEOWNERS' },
  { field: 'interfaces[0].image', value: LONG, provenance: 'inferred', source: 'cluster:manifest k8s:billing/Deployment/invoices', asOf, completeness: 'complete' },
];
const fields = (items: Record<string, unknown>[] = rows, extra = {}) => ({ items, count: items.length, total: items.length, truncated: false, ...extra });
const workload = (name: string) => ({ source: 'k8s-workloads', namespace: 'billing', kind: 'Deployment', name,
  image: { value: `ghcr.io/acme/${name}:1`, from: 'spec.template.spec.containers[0].image' } });
const handover = (name: string, w = true) => ({ from: `k8s:billing%2FDeployment%2F${name}`, to: 'invoices', link: 'inferred-from', by: 'ana@acme.example',
  revision: 'invoices@0.1.0', ...(w ? { workload: workload(name) } : {}) });

function render(props: Record<string, unknown>) {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(FieldProvenanceFold, { target, props: { asOf, ...props } });
  flushSync();
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}
const fold = (t: HTMLElement) => t.querySelector('[data-testid="field-provenance-fold"]') as HTMLDetailsElement;
const open = (t: HTMLElement) => { fold(t).open = true; flushSync(); };

describe('FieldProvenanceFold', () => {
  it('starts closed, on the details element, with the counts by provenance', () => {
    const t = render({ fields: fields() });
    expect(fold(t).tagName).toBe('DETAILS');
    expect(fold(t).open).toBe(false);
    expect(t.querySelector('.fold-title')?.textContent).toBe('Where the values come from');
    expect(t.querySelector('.fold-meta')?.textContent).toBe('2 declared, 1 accepted, 1 attested, 1 inferred');
    const t2 = render({ fields: fields([rows[0], rows[4]]) });
    expect(t2.querySelector('.fold-meta')?.textContent).toBe('1 declared, 1 inferred');
  });

  it('draws the rows in the builder order with one mark each, tipped by fieldProvenanceText', () => {
    const t = render({ fields: fields() });
    open(t);
    const got = [...t.querySelectorAll('.fpf-row')];
    expect(got.map((r) => r.querySelector('dt')?.textContent)).toEqual(['owner', 'ledger range', 'audit range', 'service.repository', 'interfaces[0].image']);
    got.forEach((r, i) => {
      expect(r.querySelectorAll('.prov-mark')).toHaveLength(1);
      expect(r.querySelector('.prov-tip')?.textContent).toBe(fieldProvenanceText(rows[i]));
    });
    expect(got[1].querySelector('.prov-tip')?.textContent).toMatch(/^Accepted by ana@acme\.example in invoices@0\.1\.0 as of .+$/);
    expect(got[2].querySelector('.prov-tip')?.textContent).toMatch(/^Proposed \^1\.4\.0 from runtime calls as of .+, partial; never declared$/);
    const long = got[4].querySelector('bdi.fpf-value') as HTMLElement;
    expect(long.textContent).toBe(LONG);
    expect(long.getAttribute('title')).toBe(LONG);
  });

  it('mutes inferred rows by colour and filters exactly them with one checkbox', () => {
    const t = render({ fields: fields() });
    open(t);
    const inferred = t.querySelector('.fpf-row[data-provenance="inferred"]') as HTMLElement;
    expect(inferred.classList.contains('fpf-inferred')).toBe(true);
    expect(getComputedStyle(inferred).opacity === '' || getComputedStyle(inferred).opacity === '1').toBe(true);
    expect(inferred.getAttribute('style') ?? '').not.toContain('opacity');
    const box = t.querySelector('[data-testid="field-provenance-show-inferred"]') as HTMLInputElement;
    expect(box.checked).toBe(true);
    box.click(); flushSync();
    expect(t.querySelectorAll('.fpf-row')).toHaveLength(4);
    expect(t.querySelector('.fpf-row[data-provenance="inferred"]')).toBeNull();
    box.click(); flushSync();
    expect(t.querySelectorAll('.fpf-row')).toHaveLength(5);
  });

  it('says how many inferred values the filter hides, even when every row is inferred', () => {
    const t = render({ fields: fields([rows[4], { ...rows[4], field: 'workload' }]) });
    open(t);
    expect(t.querySelector('[data-testid="field-provenance-inferred-hidden"]')).toBeNull();
    (t.querySelector('[data-testid="field-provenance-show-inferred"]') as HTMLInputElement).click(); flushSync();
    expect(t.querySelectorAll('.fpf-row')).toHaveLength(0);
    expect(t.querySelector('[data-testid="field-provenance-inferred-hidden"]')?.textContent).toBe('2 inferred values hidden');
    expect(t.querySelector('.fold-meta')?.textContent).toBe('2 inferred');
  });

  it('says how many replacements the bound left out', () => {
    const t = render({ fields: null, replaces: [handover('a'), handover('b')], replacesTotal: 260 });
    open(t);
    expect(t.querySelectorAll('[data-testid="field-provenance-replacement"]')).toHaveLength(2);
    expect(t.querySelector('[data-testid="field-provenance-replaces-more"]')?.textContent).toBe('258 more');
    const all = render({ fields: null, replaces: [handover('a')], replacesTotal: 1 });
    open(all);
    expect(all.querySelector('[data-testid="field-provenance-replaces-more"]')).toBeNull();
  });

  it('says what a truncated preview leaves out', () => {
    const many = Array.from({ length: 200 }, (_, i) => ({ field: `f${i}`, value: 'v', provenance: i % 2 ? 'inferred' : 'declared' }));
    const t = render({ fields: fields(many, { total: 1200, count: 200, truncated: true }) });
    expect(t.querySelector('.fold-meta')?.textContent).toBe('100 declared, 100 inferred of the first 200');
    open(t);
    const text = t.textContent || '';
    expect(text).toContain('Showing 200 of 1,200');
    expect(text).toContain('1,000 more');
    expect([...t.querySelectorAll('[data-nofilter]')].map((e) => e.textContent)).toEqual(['Showing 200 of 1,200', '1,000 more']);
  });

  it('counts the values and replacements the viewer cannot see', () => {
    const t = render({ fields: fields(rows, { hidden: 3 }), replacesHidden: 2 });
    open(t);
    expect(t.querySelector('[data-testid="field-provenance-hidden"]')?.textContent).toBe('3 values hidden by your permissions');
    expect(t.querySelector('[data-testid="field-provenance-replaces-hidden"]')?.textContent).toBe('Replaces 2 inferred services whose workload you cannot see');
  });

  it('shows the facts of the newest replaced workload only, each other replacement as one line', () => {
    const t = render({ fields: fields(), replaces: [handover('a'), handover('b'), handover('c')] });
    open(t);
    expect(t.querySelectorAll('[data-testid="workload-facts"]')).toHaveLength(1);
    expect(t.querySelector('[data-testid="inferred-image"]')?.textContent).toBe('ghcr.io/acme/a:1');
    const lines = [...t.querySelectorAll('[data-testid="field-provenance-replacement"]')].map((p) => p.textContent);
    expect(lines).toEqual(['a', 'b', 'c'].map((n) => `Declared by ana@acme.example from inferred k8s:billing/Deployment/${n} (revision invoices@0.1.0)`));
  });

  it('says when Pacto read no workload for a replaced service', () => {
    const t = render({ fields: null, replaces: [handover('a', false)] });
    open(t);
    expect(t.querySelector('[data-testid="field-provenance-replacement"]')?.textContent)
      .toBe('Declared by ana@acme.example from inferred k8s:billing/Deployment/a (revision invoices@0.1.0). Pacto read no workload for it here');
    expect(t.querySelector('[data-testid="workload-facts"]')).toBeNull();
    expect(t.querySelector('[data-testid="field-provenance-show-inferred"]')).toBeNull();
  });
});

describe('FieldProvenanceFold — contract completeness (509-S12)', () => {
  const sec = (section: string, state: string, reason: string, extra: Record<string, string> = {}): { section: string; state: string; reason: string; declare?: string; note?: string } => ({ section, state, reason, ...extra });
  const completeness = {
    asOf, completeness: 'complete', percentComplete: 60,
    sections: [
      sec('interfaces', 'declared', 'pacto.yaml declares it'),
      sec('configuration', 'not-applicable', 'pacto.yaml declares none and Pacto inferred none'),
      sec('dependencies', 'inferred', 'read from runtime calls', { declare: 'dependencies:\n  - ref: ledger\n    compatibility: ^2.3.0\n' }),
      sec('owner', 'declared', 'pacto.yaml declares it'),
      sec('summary', 'missing', 'pacto.yaml says nothing'),
      sec('domain', 'declared', 'pacto.yaml declares it'),
      sec('runtime', 'unknown', 'Pacto read no workload', { note: 'it runs nowhere' }),
      sec('readiness', 'declared', 'pacto.yaml declares it'),
    ],
  };

  it('puts the percentage and the states in the meta, and renders without field rows', () => {
    const t = render({ completeness });
    expect(t.querySelector('.fold-meta')?.textContent).toBe('60% complete · 4 declared, 1 inferred only, 1 missing, 1 unknown');
    const t2 = render({ completeness: { ...completeness, percentComplete: undefined }, fields: fields([rows[0]]) });
    expect(t2.querySelector('.fold-meta')?.textContent).toBe('Completeness unknown · 4 declared, 1 inferred only, 1 missing, 1 unknown · 1 declared');
  });

  it('draws the sections that apply first, one mark each whose tip says the state and why', () => {
    const t = render({ completeness, fields: fields([rows[0]]) });
    open(t);
    const got = [...t.querySelectorAll('[data-testid="contract-completeness"] .fpf-row')];
    expect(got.map((r) => r.querySelector('dt')?.textContent)).toEqual(['Interface documents', 'Dependency ranges', 'Owner', 'Summary', 'Domain', 'Health and runtime', 'Readiness']);
    got.forEach((r) => expect(r.querySelectorAll('.prov-mark')).toHaveLength(1));
    expect(got[1].querySelector('.prov-tip')?.textContent).toBe('Inferred only: read from runtime calls');
    expect(got[5].querySelector('.prov-tip')?.textContent).toBe('Unknown: Pacto read no workload; it runs nowhere');
    // The rows come before the field rows.
    const all = [...t.querySelectorAll('.fpf-row')];
    expect(all.indexOf(got[0])).toBe(0);
    expect(got[1].classList.contains('fpf-inferred')).toBe(true);
  });

  it('offers the YAML to declare a gap, with a copy button', async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const t = render({ completeness });
    open(t);
    const yaml = [...t.querySelectorAll('[data-testid="completeness-declare"]')];
    expect(yaml).toHaveLength(1);
    expect(yaml[0].querySelector('summary')?.textContent).toBe('YAML to declare it');
    expect(yaml[0].querySelector('code')?.textContent).toContain('compatibility: ^2.3.0');
    (yaml[0].querySelector('button') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(yaml[0].querySelector('button')?.textContent).toBe('Copied'));
    expect(writeText).toHaveBeenCalledWith(completeness.sections[2].declare);
  });
});
