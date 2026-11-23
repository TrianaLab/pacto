/**
 * The revision page's Capabilities list names each extension by its ref and, when a
 * trusted producer reported on it, says covered, failing or unknown with a discreet mark
 * naming the producer, key and time. A row nobody reported on adds nothing.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import RevisionEntity from './RevisionEntity.svelte';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const preview = (items: unknown[], extra = {}) => ({ total: items.length, count: items.length, truncated: false, items, ...extra });

function detail(rev: Record<string, unknown>) {
  return { entity: { kind: 'revision', key: 'd/payments@sha256:1' }, revision: {
    service: { kind: 'service', key: 'd/payments', label: 'payments' }, valid: true, identity: {}, provenance: {},
    validation: preview([]), interfaces: preview([]), configurations: preview([]), policies: preview([]),
    capabilities: preview([]), dependencies: preview([]), tools: preview([]), skills: preview([]), docs: preview([]),
    metadata: preview([]), exactTargets: preview([]), inferredTargets: preview([]), environments: preview([]), stages: [],
    breaks: preview([]),
    ...rev,
  } };
}
const open = (title: string) => {
  flushSync();
  const s = [...target.querySelectorAll('details')].find((d) => d.querySelector('summary')?.textContent?.includes(title)) as HTMLDetailsElement;
  s.open = true;
  s.dispatchEvent(new Event('toggle'));
  flushSync();
  return s;
};

const caps = [
  { type: 'extension', ref: 'x.acme.io/backup', check: { ref: 'x.acme.io/backup', state: 'covered', producer: 'acme-backup', producerKeyId: 'acme-backup-2026', asOf: '2026-11-22T03:00:00Z', source: 'evidence-prod', provenance: 'observed' } },
  { type: 'extension', ref: 'x.acme.io/restore', check: { ref: 'x.acme.io/restore', state: 'failing', producer: 'acme-backup', producerKeyId: 'acme-backup-2026', producers: ['acme-backup', 'dr-audit'], asOf: '2026-11-22T03:00:00Z', source: 'evidence-prod', reason: 'reported absent by acme-backup', provenance: 'observed' } },
  { type: 'extension', ref: 'x.acme.io/audit', check: { ref: 'x.acme.io/audit', state: 'unknown', producer: 'dr-audit', stale: true, source: 'evidence-prod', reason: 'the last report is older than the freshness window', provenance: 'observed' } },
  { type: 'extension', ref: 'x.acme.io/quota' },
  { type: 'health' },
];

describe('RevisionEntity, extension checks', () => {
  it('names each extension by its ref and says who checks it', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ capabilities: preview(caps) }) } });
    const rows = [...open('Capabilities').querySelectorAll('.re-caps li')] as HTMLElement[];
    expect(rows.length).toBe(5);
    expect(rows.slice(0, 4).map((r) => r.querySelector('.ri-name')!.textContent)).toEqual(
      ['x.acme.io/backup', 'x.acme.io/restore', 'x.acme.io/audit', 'x.acme.io/quota']);
    expect(rows[4].querySelector('.ri-name')!.textContent).toBe('health');

    const label = (r: HTMLElement) => r.querySelector('[data-testid="ext-check"]')?.textContent?.trim();
    expect(label(rows[0])).toBe('Checked by acme-backup');
    expect(label(rows[1])).toBe('Reported absent by acme-backup and dr-audit');
    expect(label(rows[2])).toBe('Unknown');
    expect(rows[0].querySelector('.re-ext-covered')).toBeTruthy();
    expect(rows[1].querySelector('.re-ext-failing')).toBeTruthy();
    expect(rows[2].querySelector('.re-ext-unknown')).toBeTruthy();

    // A row nobody reported on, and a standard capability, add nothing.
    for (const r of [rows[3], rows[4]]) {
      expect(r.querySelector('[data-testid="ext-check"]')).toBeNull();
      expect(r.querySelector('.prov-mark')).toBeNull();
    }

    const tip = (r: HTMLElement) => r.querySelector('.prov-mark [role="note"]')!.textContent!;
    expect(rows[0].querySelector('button[aria-label="About x.acme.io/backup"]')).toBeTruthy();
    expect(tip(rows[0])).toContain('acme-backup (key acme-backup-2026) reported it present');
    expect(tip(rows[0])).toContain('through a signed report.');
    expect(tip(rows[0])).toContain('Source: evidence-prod.');
    expect(tip(rows[1])).toContain('acme-backup (key acme-backup-2026) reported it absent');
    expect(tip(rows[2])).toBe('The last report is older than the freshness window. Source: evidence-prod.');
  });
});
