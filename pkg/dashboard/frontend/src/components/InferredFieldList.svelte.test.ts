/**
 * Inferred values read apart from declared ones: one muted row per value, a discreet
 * mark whose note names the source and confidence, the first five shown and the rest
 * one disclosure away.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import InferredFieldList from './InferredFieldList.svelte';
// @ts-expect-error — a raw import has no declaration file
import source from './InferredFieldList.svelte?raw';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const field = (i: number, extra = {}) => ({ field: `configuration.KEY_${i}`, value: 'string', source: `deploy/app.yaml:${i + 1}`, confidence: 'likely', provenance: 'inferred', ...extra });
const render = (props: Record<string, unknown>) => { comp = mount(InferredFieldList, { target, props }); flushSync(); };
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
    expect(source).toMatch(/\.ifl-row code, \.ifl-value \{ overflow-wrap: anywhere; min-inline-size: 0; \}/);
  });

  it('says the record could not be read and lists no row', () => {
    render({ items: [field(0)], total: 1, unreadable: true });
    expect(target.textContent).toContain('Pacto could not read the inferred record');
    expect(rows().length).toBe(0);
  });

  it('says how many fields the bounded list leaves out', () => {
    render({ items: Array.from({ length: 50 }, (_, i) => field(i)), total: 60 });
    expect(target.querySelector('summary')!.textContent).toContain('55 more');
    expect(target.textContent).toContain('Showing 50 of 60.');
  });
});
