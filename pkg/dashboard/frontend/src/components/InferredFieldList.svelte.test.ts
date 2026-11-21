/**
 * Inferred values read apart from declared ones: one muted row per value, a discreet
 * mark whose note names the source and confidence, the first five shown and the rest
 * one disclosure away.
 */
import { formatWhen } from '../lib/dateFormat.ts';
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import InferredFieldList from './InferredFieldList.svelte';
import { ownerProvenanceText } from '../lib/entityLabels.ts';
// @ts-expect-error — a raw import has no declaration file
import source from './InferredFieldList.svelte?raw';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const field = (i: number, extra = {}) => ({ field: `configuration.KEY_${i}`, value: 'string', source: `deploy/app.yaml:${i + 1}`, confidence: 'likely', provenance: 'inferred', ...extra });
const render = (props: Record<string, unknown>) => { comp = mount(InferredFieldList, { target, props }); flushSync(); };
const render0 = () => { if (comp) { unmount(comp); comp = null; } };
const rows = () => [...target.querySelectorAll('[data-testid="inferred-field"]')] as HTMLElement[];
const visible = (el: Element) => {
  const c = el.cloneNode(true) as Element;
  c.querySelectorAll('[role="note"]').forEach((n) => n.remove());
  return c.textContent ?? '';
};

describe('InferredFieldList', () => {
  it('shows five rows and puts the rest under one "3 more" disclosure', () => {
    const items = Array.from({ length: 8 }, (_, i) => field(i));
    render({ items, total: 8 });
    const details = target.querySelectorAll('details');
    expect(details.length).toBe(1);
    expect(details[0].querySelector('summary')!.textContent).toContain('3 more');
    expect(rows().filter((r) => !r.closest('details')).length).toBe(5);
    expect(rows().length).toBe(8);
  });

  it('names each mark after its field and puts the source and confidence in the note', () => {
    render({ items: [field(0, { field: 'configuration.PORT', source: 'deploy/app.yaml:24' })], total: 1 });
    const btn = target.querySelector('button[aria-label="About inferred configuration.PORT"]');
    expect(btn).toBeTruthy();
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).toContain('Source: deploy/app.yaml:24');
    expect(note).toContain('Confidence: likely');
  });

  it('omits the confidence when the field has none', () => {
    render({ items: [field(0, { field: 'service.name', source: 'chart/Chart.yaml:2', confidence: undefined })], total: 1 });
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).toContain('Source: chart/Chart.yaml:2');
    expect(note).not.toContain('Confidence');
  });

  it('prints no "inferred" text in a row besides the mark', () => {
    render({ items: [field(0), field(1)], total: 2 });
    for (const r of rows()) expect(visible(r).toLowerCase()).not.toContain('inferred');
    expect(target.querySelectorAll('.tag').length).toBe(0);
  });

  it('isolates a long key and lets it break anywhere', () => {
    const key = `configuration.${'A'.repeat(66)}`;
    expect(key.length).toBe(80);
    render({ items: [field(0, { field: key })], total: 1 });
    const bdi = target.querySelector('code bdi')!;
    expect(bdi.textContent).toBe(key);
    // jsdom applies no component style, so the rule is read from the source.
    expect(source).toMatch(/\.ifl-text code, \.ifl-value \{ overflow-wrap: anywhere; min-inline-size: 0; \}/);
  });

  it('says the record could not be read and keeps the rows a local read infers', () => {
    render({ items: [field(0, { field: 'service.name', source: 'chart/Chart.yaml:2' })], total: 1, unreadable: true });
    expect(target.textContent).toContain('Pacto could not read the inferred record');
    expect(rows().length).toBe(1);
    render0();
    render({ items: [], total: 0, unreadable: true });
    expect(target.textContent).toContain('Pacto could not read the inferred record');
    expect(target.querySelectorAll('ul').length).toBe(0);
  });

  it('opens each row with its mark and keeps each row a touch target tall', () => {
    render({ items: [field(0)], total: 1 });
    expect(rows()[0].firstElementChild!.classList.contains('prov-mark')).toBe(true);
    // The mark opens the row, so its tip grows rightward and stays in a phone viewport.
    expect(rows()[0].firstElementChild!.classList.contains('prov-start')).toBe(true);
    // jsdom applies no component style, so the rules are read from the source.
    expect(source).toMatch(/\.ifl-row \{[^}]*min-block-size: var\(--touch-min\)/);
    expect(source).toMatch(/@media \(pointer: coarse\) \{ \.ifl-row \{ min-block-size: 44px; \} \}/);
  });

  it('says how many fields the bounded list leaves out', () => {
    render({ items: Array.from({ length: 50 }, (_, i) => field(i)), total: 60 });
    expect(target.querySelector('summary')!.textContent).toContain('55 more');
    expect(target.textContent).toContain('Showing 50 of 60.');
  });
  it('names the owner file and the outranked sources in the note and adds no chip', () => {
    render({ items: [field(0, { field: 'service.owner', value: 'team acme/team-payments', source: 'catalog-info.yaml:9', class: 'ownerFile', outranks: ['repository:.github/CODEOWNERS:4', 'deploy/deployment.yaml:7'] })], total: 1 });
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).toContain('Read from catalog-info.yaml:9; not verified; never clears a gate.');
    expect(note).toContain('Outranks .github/CODEOWNERS:4 (repository file, not in the bundle), deploy/deployment.yaml:7.');
    expect(rows()[0].children.length).toBe(2);
    expect(target.querySelectorAll('.tag').length).toBe(0);
  });

  it('says an owner file row the revision signature attests is attested, never "not verified"', () => {
    render({ items: [field(0, { field: 'service.owner', source: 'catalog-info.yaml:7', class: 'ownerFile', attested: true })], total: 1 });
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).toContain(`Attested: ${ownerProvenanceText('attested')}.`);
    expect(note).not.toContain('not verified');
  });

  it('counts the outranked sources the server left out', () => {
    render({ items: [field(0, { outranks: ['a.yaml:1', 'b.yaml:2'], outranksTotal: 299 })], total: 1 });
    expect(target.querySelector('[role="note"]')!.textContent).toContain('Outranks a.yaml:1, b.yaml:2 and 297 more.');
  });

  it('says a repository source is a repository file outside the bundle', () => {
    render({ items: [field(0, { field: 'service.owner', source: 'repository:.github/CODEOWNERS:4', class: 'ownerFile' })], total: 1 });
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).toContain('Source: .github/CODEOWNERS:4 (repository file, not in the bundle).');
    expect(note).not.toContain('repository:');
  });

  it('says nothing about an owner file or outranks on a plain row', () => {
    render({ items: [field(0)], total: 1 });
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).not.toContain('not verified');
    expect(note).not.toContain('Outranks');
  });
});

describe('InferredFieldList, cluster facts', () => {
  it('says when a cluster fact was read, that it is partial and what it proposes', () => {
    render({ items: [field(0, { field: 'dependencies[payments]', source: 'cluster:billing/Deployment/invoices', asOf: '2026-11-17T10:00:00Z', completeness: 'partial', proposed: '^2.0.0' })], total: 1 });
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).toContain(`Read from the cluster ${formatWhen('2026-11-17T10:00:00Z')}.`);
    expect(note).not.toContain('2026-11-17T10:00:00Z');
    expect(note).toContain('Partial: some nodes, pods or supporting sources were not observed.');
    expect(note).toContain('Proposed ^2.0.0, not declared.');
  });

  it('leaves a folder row note unchanged', () => {
    render({ items: [field(0)], total: 1 });
    const note = target.querySelector('[role="note"]')!.textContent!;
    expect(note).toBe('Inferred, not declared. Source: deploy/app.yaml:1. Confidence: likely.');
  });
});
