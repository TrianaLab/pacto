/**
 * The files discovery recognized but did not read, on the revision page: one collapsed
 * disclosure after the inferred fields, shown even when nothing was inferred, bounded,
 * and saying when discovery stopped early.
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
    breaks: preview([]), discoveryFindings: { items: [], total: 0 },
    ...rev,
  } };
}
const finding = (i: number) => ({ code: 'DISCOVERY_UNSUPPORTED_FILE', source: `deploy/very/long/path/to/helmfile-${i}.yaml`, message: `helmfile-${i}.yaml is not a format Pacto reads` });
const render = (rev: Record<string, unknown>) => { comp = mount(RevisionEntity, { target, props: { detail: detail(rev) } }); flushSync(); };
const box = () => target.querySelector('[data-testid="revision-discovery-findings"]') as HTMLDetailsElement | null;

describe('RevisionEntity, discovery findings', () => {
  it('is closed by default and opens', () => {
    render({ inferred: [{ field: 'ports[8080]', value: '8080/tcp', source: 'Dockerfile:4', provenance: 'inferred' }], discoveryFindings: { items: [finding(0)], total: 1 } });
    const d = box()!;
    expect(d.open).toBe(false);
    expect(d.querySelector('summary')!.textContent).toContain('1 discovery finding');
    d.open = true;
    flushSync();
    expect(d.open).toBe(true);
    expect(d.textContent).toContain('DISCOVERY_UNSUPPORTED_FILE');
    expect(d.textContent).toContain('helmfile-0.yaml is not a format Pacto reads');
    // After the field list, outside its rows.
    expect(d.closest('[data-testid="inferred-field"]')).toBeNull();
    expect(d.closest('[data-testid="revision-inferred"]')).toBeTruthy();
  });

  it('bounds the rows and counts the rest', () => {
    render({ discoveryFindings: { items: Array.from({ length: 20 }, (_, i) => finding(i)), total: 100 } });
    const d = box()!;
    expect(d.querySelectorAll('li').length).toBe(20);
    expect(d.querySelector('summary')!.textContent).toContain('100 discovery findings');
    expect(d.textContent).toContain('and 80 more');
  });

  it('isolates the source with a title and a safe break', () => {
    render({ discoveryFindings: { items: [finding(3)], total: 1 } });
    const bdi = box()!.querySelector('bdi')!;
    expect(bdi.textContent).toBe('deploy/very/long/path/to/helmfile-3.yaml');
    expect(bdi.getAttribute('title')).toBe('deploy/very/long/path/to/helmfile-3.yaml');
    expect(bdi.classList.contains('re-path')).toBe(true);
  });

  it('renders the section for findings with no inferred field', () => {
    render({ inferred: [], discoveryFindings: { items: [finding(0), finding(1)], total: 2 } });
    expect(target.querySelector('[data-testid="revision-inferred"]')).toBeTruthy();
    expect(box()!.querySelector('summary')!.textContent).toContain('2 discovery findings');
    expect(target.querySelectorAll('[data-testid="inferred-field"]').length).toBe(0);
  });

  it('lists two findings of one line, with the same code and source', () => {
    const twice = { code: 'DISCOVERY_UNRESOLVED', source: 'Dockerfile:5', message: 'EXPOSE at Dockerfile:5 names a port through a variable or a range, so it is not read' };
    render({ discoveryFindings: { items: [twice, { ...twice }], total: 2 } });
    expect(box()!.querySelectorAll('li').length).toBe(2);
  });

  it('renders no section with neither inferred fields nor findings', () => {
    render({ inferred: [] });
    expect(target.querySelector('[data-testid="revision-inferred"]')).toBeNull();
    expect(box()).toBeNull();
  });

  it('says the results are partial when discovery stopped early', () => {
    render({ discoveryFindings: { items: [{ code: 'DISCOVERY_TRUNCATED', source: '.', message: 'discovery stopped' }, finding(0)], total: 2, truncated: true } });
    const s = box()!.querySelector('summary')!.textContent!;
    expect(s).toContain('discovery stopped after 5000 files; results are partial');
    expect(s).not.toContain('discovery findings');
  });
});
