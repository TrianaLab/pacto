/**
 * A minimal contract (pactoVersion 2.5) on the revision page: every value Pacto implied
 * shows where it came from, an undeclared version says so, an interface shows its port
 * and the announced breaks are a bounded list whose hidden consumers are counted only.
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

describe('RevisionEntity, minimal contract', () => {
  it('shows each inferred value with its source and says the version is not declared', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ inferred: [
      { field: 'service.name', value: 'payments', source: 'chart/Chart.yaml:2', provenance: 'inferred' },
      { field: 'pactoVersion', value: '2.5', source: 'this CLI', provenance: 'inferred' },
    ] }) } });
    const block = target.querySelector('[data-testid="revision-inferred"]')!;
    expect(block.textContent).toContain('service.name');
    expect(block.textContent).toContain('payments');
    expect(block.textContent).toContain('inferred from chart/Chart.yaml:2');
    expect(block.textContent).toContain('inferred from this CLI');
    expect(block.querySelectorAll('.tag').length).toBe(2);
    expect(target.textContent).toContain('Version not declared');
  });

  it('shows the declared version and no inferred block for a declared contract', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ version: '1.2.0' }) } });
    expect(target.querySelector('[data-testid="revision-inferred"]')).toBeNull();
    expect(target.textContent).not.toContain('Version not declared');
  });

  it('shows the port an interface declares', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ version: '1.0.0', interfaces: preview([
      { name: 'api', type: 'openapi', port: 8080, operationsKnown: true, operations: preview([]) },
    ]) }) } });
    open('Interfaces');
    expect(target.querySelector('.re-ifaces')!.textContent).toContain(':8080');
  });

  it('lists announced breaks bounded, linking the consumers the fleet holds', () => {
    const items = Array.from({ length: 20 }, (_, i) => ({
      consumer: `consumer-${i}`, reason: 'drops the v1 settlement endpoint', expires: '2026-12-14',
      ...(i === 0 ? { service: { kind: 'service', key: 'd/consumer-0', label: 'consumer-0', href: '/fleet/services/d%2Fconsumer-0' } } : {}),
    }));
    comp = mount(RevisionEntity, { target, props: { detail: detail({ version: '1.0.0', breaks: { total: 25, count: 20, truncated: true, items } }) } });
    const sec = open('Announced breaks');
    expect(sec.querySelectorAll('[data-testid="revision-break"]').length).toBe(20);
    expect(sec.querySelector('a[href*="consumer-0"]')).toBeTruthy();
    expect(sec.textContent).toContain('consumer-1');
    expect(sec.textContent).toContain('drops the v1 settlement endpoint');
    expect(sec.textContent).toContain('Dec 14, 2026');
    expect(sec.textContent).toContain('Showing 20 of 25.');
  });

  it('counts the break consumers the viewer may not see without naming them', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ version: '1.0.0', breaks: { total: 3, count: 1, truncated: false, hidden: 2, items: [{ consumer: 'reports', reason: 'renamed' }] } }) } });
    const sec = open('Announced breaks');
    expect(sec.textContent).toContain('2 consumers hidden by your Kubernetes permissions.');
  });

  it('has no breaks section when nothing is announced', () => {
    comp = mount(RevisionEntity, { target, props: { detail: detail({ version: '1.0.0' }) } });
    expect(target.textContent).not.toContain('Announced breaks');
  });
});
