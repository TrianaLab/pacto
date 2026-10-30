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
    ]) }) } });
    expect(target.textContent).toContain('1 deprecated and 1 past sunset');
    open('Interfaces');
    const text = target.textContent!;
    expect(text).toContain('Past sunset');
    expect(text).toContain('Deprecated Jan 1, 2026 · sunset Jun 1, 2026 · successor orders-v2');
    expect(text).toContain('Epoch 2');
    expect(target.querySelector('dl[aria-label="Guarantees of orders"]')?.textContent).toContain('per-key');
  });

  const stages = [
    { name: 'structural', status: 'pass' }, { name: 'cross-field', status: 'pass' }, { name: 'policy', status: 'fail' }, { name: 'environments', status: 'skip', reason: 'policy failed' },
  ];
  const stageTags = () => [...target.querySelectorAll('[data-testid="revision-stages"] .tag')];
  it('lists the validation stages', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ valid: false, stages, environments: preview([env('staging', 'skip', { reason: 'policy failed' })]) }) } });
    expect(stageTags().map((t) => t.textContent)).toEqual(['Structure passed', 'Cross-field passed', 'Policy failed', 'Environments not run']);
    expect(stageTags()[3].getAttribute('title')).toBe('Environments: policy failed');
    expect(target.textContent).toContain('Not checked: policy failed.');
  });

  it('leaves out the environments stage of a revision that declares none', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ stages }) } });
    expect(stageTags().map((t) => t.textContent)).toEqual(['Structure passed', 'Cross-field passed', 'Policy failed']);
  });

  it('opens the environments section when one failed or could not be checked', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ environments: preview([
      env('staging', 'pass'),
      env('production', 'fail', { findings: preview([{ code: 'ENVIRONMENT_VALUES_INVALID', severity: 'error', path: 'environments[1].file', message: 'bad replicas', file: 'envs/production.yaml', line: 2 }]) }),
      env('dev', 'unverified'),
    ]) }) } });
    expect(target.textContent).toContain('1 failed and 1 not verified');
    expect(target.textContent).toContain('envs/production.yaml:2');
    expect(target.querySelector('[data-testid="revision-declares-none"]')?.textContent).not.toContain('environments');
  });

  it('keeps a clean run closed and says so', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ environments: preview([env('staging', 'pass')]) }) } });
    expect(target.textContent).toContain('Every environment passed');
    expect(target.querySelector('[data-testid="environment"]')).toBeNull();
  });

  it('says when a revision declares no environments', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({}) } });
    expect(target.querySelector('[data-testid="revision-declares-none"]')?.textContent).toContain('environments');
    expect(target.querySelector('[data-testid="revision-stages"]')).toBeNull();
  });
});
