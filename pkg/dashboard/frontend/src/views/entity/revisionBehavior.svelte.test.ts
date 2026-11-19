/**
 * The revision page names what pactoVersion 2.2 and 2.3 declare: each interface's
 * lifecycle, guarantees and epoch, the validation stages and every environment with
 * its check. "Declares no environments" and "could not verify" never read alike.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import RevisionEntity from './RevisionEntity.svelte';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(new Date('2026-10-30T12:00:00Z')); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); vi.useRealTimers(); });

const preview = (items: unknown[], extra = {}) => ({ total: items.length, count: items.length, truncated: false, items, ...extra });
const env = (name: string, status: string, extra = {}) => ({ name, file: `envs/${name}.yaml`, status, findings: preview([]), ...extra });

function detail(rev: Record<string, unknown>) {
  return { entity: { kind: 'revision', key: 'd/events@sha256:1' }, revision: {
    service: { kind: 'service', key: 'd/events', label: 'events' }, valid: true, identity: {}, provenance: {},
    validation: preview([]), interfaces: preview([]), configurations: preview([]), policies: preview([]),
    capabilities: preview([]), dependencies: preview([]), tools: preview([]), skills: preview([]), docs: preview([]),
    metadata: preview([]), exactTargets: preview([]), inferredTargets: preview([]), environments: preview([]), stages: [],
    ...rev,
  } };
}
const open = (title: string) => {
  flushSync(); // the section's own effects run first, then the reader opens it
  const s = [...target.querySelectorAll('details')].find((d) => d.querySelector('summary')?.textContent?.includes(title)) as HTMLDetailsElement;
  s.open = true;
  s.dispatchEvent(new Event('toggle'));
  flushSync();
  return s;
};

describe('RevisionEntity behavior', () => {
  it('marks deprecated interfaces, their guarantees and epoch', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ interfaces: preview([
      { name: 'orders', type: 'asyncapi', operationsKnown: true, operations: preview([]), lifecycle: { deprecated: '2026-01-01', sunset: '2026-06-01', successor: 'orders-v2' }, guarantees: { delivery: 'at-least-once', ordering: 'per-key' }, compatibilityEpoch: 2 },
      { name: 'orders-v2', type: 'asyncapi', operationsKnown: true, operations: preview([]), lifecycle: { deprecated: '2026-09-01', sunset: '2027-01-01' } },
      { name: 'plain', type: 'openapi', operationsKnown: true, operations: preview([]) },
    ], { deprecated: 2, pastSunset: 1 }) }) } });
    expect(target.textContent).toContain('1 deprecated and 1 past sunset');
    open('Interfaces');
    const text = target.textContent!;
    expect(text).toContain('Past sunset');
    expect(text).toContain('Deprecated Jan 1, 2026 · sunset Jun 1, 2026 · successor orders-v2');
    expect(text).toContain('Epoch 2');
    expect(target.querySelector('[role="group"][aria-label="Guarantees of orders"] dl')?.textContent).toContain('per-key');
  });

  const stages = [
    { name: 'structural', status: 'pass' }, { name: 'cross-field', status: 'pass' }, { name: 'policy', status: 'fail' },
    { name: 'configuration-refs', status: 'skip', reason: 'policy failed' }, { name: 'environments', status: 'skip', reason: 'policy failed' },
  ];
  const stageTags = () => [...target.querySelectorAll('[data-testid="revision-stages"] .tag')];
  it('lists the validation stages', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ valid: false, stages, environments: preview([env('staging', 'skip', { reason: 'policy failed' })], { skipped: 1 }) }) } });
    expect(stageTags().map((t) => t.textContent)).toEqual(['Structure passed', 'Cross-field passed', 'Policy failed', 'Configuration references not run', 'Environments not run']);
    expect(stageTags()[4].getAttribute('title')).toBe('Environments: policy failed');
    expect(target.textContent).toContain('Not checked: policy failed.');
  });

  it('leaves out the environments stage of a revision that declares none', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ stages }) } });
    expect(stageTags().map((t) => t.textContent)).toEqual(['Structure passed', 'Cross-field passed', 'Policy failed', 'Configuration references not run']);
  });

  it('leaves out the configuration-refs stage of a revision that references no configuration', () => {
    const none = [...stages.slice(0, 3), { name: 'configuration-refs', status: 'skip', reason: 'no configuration references' }];
    comp = mount(RevisionEntity, { target, props: { detail: detail({ stages: none }) } });
    expect(stageTags().map((t) => t.textContent)).toEqual(['Structure passed', 'Cross-field passed', 'Policy failed']);
  });

  it('opens the environments section when one failed or could not be checked', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ environments: preview([
      env('staging', 'pass'),
      env('production', 'fail', { findings: preview([{ code: 'ENVIRONMENT_VALUES_INVALID', severity: 'error', path: 'environments[1].file', message: 'bad replicas', file: 'envs/production.yaml', line: 2 }]) }),
      env('dev', 'unverified'),
    ], { failed: 1, unverified: 1 }) }) } });
    expect(target.textContent).toContain('1 failed and 1 not verified');
    expect(target.textContent).toContain('envs/production.yaml:2');
    expect(target.querySelector('[data-testid="revision-declares-none"]')?.textContent).not.toContain('environments');
  });

  it('keeps a clean run closed and says so', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ environments: preview([env('staging', 'pass')]) }) } });
    expect(target.textContent).toContain('Every environment passed');
    expect(target.querySelector('[data-testid="environment"]')).toBeNull();
  });

  it('counts every environment, not the preview it shows', () => {
    const shown = Array.from({ length: 200 }, (_, i) => env(`env-${i}`, 'pass'));
    comp = mount(RevisionEntity, { target, props: { detail: detail({ environments: preview(shown, { total: 300, truncated: true, failed: 1 }) }) } });
    expect(target.textContent).toContain('1 failed');
    expect(target.textContent).not.toContain('Every environment passed');
  });

  it('says when a revision declares no environments', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({}) } });
    expect(target.querySelector('[data-testid="revision-declares-none"]')?.textContent).toContain('environments');
    expect(target.querySelector('[data-testid="revision-stages"]')).toBeNull();
  });

  // pacto ci raised the authored pactoVersion in memory; the bundle's pacto.inferred.yaml
  // records it. The fact says so muted, with one provenance mark that ends the row.
  const pactoFact = () => [...target.querySelectorAll('.re-fact')].find((f) => f.querySelector('.re-k')?.textContent === 'Pacto version') as HTMLElement;
  it('shows migrated from beside the Pacto version with one mark', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ pactoVersion: '2.5', migratedFrom: '2.4' }) } });
    const f = pactoFact();
    expect(f.textContent).toContain('2.5');
    expect(f.textContent).toContain('migrated from 2.4');
    const marks = f.querySelectorAll('.prov-mark');
    expect(marks).toHaveLength(1);
    expect(marks[0].querySelector('button')?.getAttribute('aria-label')).toBe('inferred pactoVersion migration');
    expect(marks[0].querySelector('.prov-tip')?.textContent).toContain('pacto.inferred.yaml');
    expect(f.querySelector('.tag')).toBeNull();
  });

  it('a migrated and behind revision shows both', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ pactoVersion: '2.5', migratedFrom: '2.4', pactoVersionCurrent: '99.0' }) } });
    const f = pactoFact();
    expect(f.querySelector('.tag')?.textContent).toBe('behind 99.0');
    expect(f.lastElementChild?.classList.contains('prov-mark')).toBe(true);
    expect(f.querySelector('.tag')!.compareDocumentPosition(f.querySelector('.prov-mark')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('the migrated text is muted', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ pactoVersion: '2.5', migratedFrom: '2.4' }) } });
    const muted = pactoFact().querySelector('.re-muted');
    expect(muted?.textContent).toBe('migrated from 2.4');
  });

  it('the tip names the recorded versions', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ pactoVersion: '2.5', migratedFrom: '2.3' }) } });
    const tip = pactoFact().querySelector('.prov-tip')?.textContent ?? '';
    expect(tip).toContain('pactoVersion 2.3');
    expect(tip).toContain('published as 2.5');
  });

  it('no migratedFrom renders today\'s fact', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ pactoVersion: '2.5' }) } });
    const f = pactoFact();
    expect(f.textContent).toBe('Pacto version2.5');
    expect(f.textContent).not.toContain('migrated from');
    expect(f.querySelector('.prov-mark')).toBeNull();
  });
});
