/**
 * Who declared a revision, on the revision page: one muted line in the Inferred block
 * with one mark, shown even when every cluster field was dropped, naming no subject for
 * an unverified signer and no namespace the viewer may not read.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import RevisionEntity from './RevisionEntity.svelte';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const preview = (items: unknown[]) => ({ total: items.length, count: items.length, truncated: false, items });
function detail(rev: Record<string, unknown>) {
  return { entity: { kind: 'revision', key: 'billing/invoices@sha256:1' }, revision: {
    service: { kind: 'service', key: 'billing/invoices', label: 'invoices' }, valid: true, identity: {}, provenance: {},
    validation: preview([]), interfaces: preview([]), configurations: preview([]), policies: preview([]),
    capabilities: preview([]), dependencies: preview([]), tools: preview([]), skills: preview([]), docs: preview([]),
    metadata: preview([]), exactTargets: preview([]), inferredTargets: preview([]), environments: preview([]), stages: [],
    breaks: preview([]), discoveryFindings: { items: [], total: 0 },
    ...rev,
  } };
}
const render = (rev: Record<string, unknown>) => { comp = mount(RevisionEntity, { target, props: { detail: detail(rev) } }); flushSync(); };
const line = () => target.querySelector('[data-testid="inferred-declaration"]') as HTMLElement | null;
const visible = (el: Element) => { const c = el.cloneNode(true) as Element; c.querySelectorAll('[role="note"]').forEach((n) => n.remove()); return c.textContent ?? ''; };
const LONG = `${'a'.repeat(70)}@acme.example.com`;
const signed = { state: 'declared', signatureChecked: true, signer: { subject: LONG, issuer: 'https://accounts.google.com', class: 'human' }, inferredFrom: 'k8s:billing/Deployment/invoices' };
const field = { field: 'dependencies[payments]', value: 'payments', source: 'cluster:billing/Deployment/invoices', provenance: 'inferred', asOf: '2026-11-17T10:00:00Z', completeness: 'partial', proposed: '^2.0.0' };

describe('RevisionEntity, declaration', () => {
  it('shows the line only with a declaration', () => {
    render({ inferred: [field], inferredTotal: 1 });
    expect(line()).toBeNull();
  });

  it('shows one line with one mark inside the Inferred block, the long subject inside bdi', () => {
    render({ inferred: [field], inferredTotal: 1, declaration: signed, inferredCoverage: [{ source: 'cluster:x', state: 'complete' }] });
    const l = line()!;
    expect(l.closest('[data-testid="revision-inferred"]')).toBeTruthy();
    expect(l.querySelectorAll('.prov-mark').length).toBe(1);
    expect(l.querySelector('button[aria-label="About this declaration"]')).toBeTruthy();
    expect(l.querySelector('bdi')!.textContent).toContain(LONG);
    expect(visible(l)).toContain('cluster read 1 of 1 source complete');
  });

  it('links the external system a claim came from', () => {
    render({ inferred: [], declaration: { ...signed, inferredFrom: 'external:stripe' } });
    const a = line()!.querySelector('bdi a') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('#/fleet/externals/stripe');
    expect(a.textContent).toBe('external stripe');
    expect(line()!.querySelector('bdi')!.textContent).toBe(`Declared by ${LONG} from external stripe`);
  });

  it('declaration with zero inferred items still renders inferred-declaration', () => {
    render({ inferred: [], inferredTotal: 0, declaration: signed });
    expect(line()).toBeTruthy();
    expect(target.querySelectorAll('[data-testid="inferred-field"]').length).toBe(0);
  });

  it('inferredFromHidden with zero items names no namespace', () => {
    render({ inferred: [], declaration: { ...signed, inferredFrom: undefined, inferredFromHidden: true }, inferredHidden: { fields: 3 } });
    const block = target.querySelector('[data-testid="revision-inferred"]')!;
    expect(line()!.textContent).toContain('a workload you may not read');
    expect(block.textContent).not.toContain('billing');
  });

  it('unverified signer line names no subject', () => {
    render({ inferred: [], declaration: { state: 'declared', signatureChecked: false, claimedSubject: 'mallory@evil.example' } });
    expect(visible(line()!)).toContain('Declaration by an unverified signer');
    expect(visible(line()!)).not.toContain('mallory');
  });

  it('a signer the installation does not allow is named by no subject', () => {
    render({ inferred: [], declaration: { state: 'declared', signatureChecked: true, claimedSubject: 'mallory@gmail.com', notAccepted: 'no installation signer policy was given to this reader' } });
    expect(visible(line()!)).toContain('Declaration by a signer this installation does not allow');
    expect(visible(line()!)).not.toContain('mallory');
  });
});
