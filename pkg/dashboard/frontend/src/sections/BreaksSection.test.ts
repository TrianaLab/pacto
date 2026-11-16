/**
 * The legacy service page (the `pacto doc` export host) for pactoVersion 2.5: announced
 * breaks, interface ports and an undeclared compatibility range. The doc export serves
 * the immutable bundles/payments-service/v2.0.1, which declares none of the three, so
 * these Vitest cases are the only coverage of BreaksSection, the port pill of
 * InterfacesSection and the unknown range of DependenciesSection.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import BreaksSection from './BreaksSection.svelte';
// @ts-expect-error — Svelte component has no declaration file
import InterfacesSection from './InterfacesSection.svelte';
// @ts-expect-error — Svelte component has no declaration file
import DependenciesSection from './DependenciesSection.svelte';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const services = [{ name: 'reports' }];

describe('BreaksSection', () => {
  it('falls back to the placeholder when nothing is announced', () => {
    comp = mount(BreaksSection, { target, props: { breaks: [], services, open: true } });
    expect(target.textContent).toContain('None declared');
  });

  it('links the consumers the fleet holds and keeps a long reason whole', () => {
    const reason = 'removes-the-v1-settlement-endpoint-'.repeat(6);
    comp = mount(BreaksSection, { target, props: { open: true, services, breaks: [
      { consumers: ['reports', 'ledger'], reason, expires: '2026-12-14' },
    ] } });
    expect(target.querySelector('a[href*="reports"]')?.textContent).toBe('reports');
    expect(target.querySelector('a[href*="ledger"]')).toBeNull();
    expect(target.textContent).toContain('ledger');
    expect(target.textContent).toContain(reason);
    expect(target.textContent).toContain('Dec 14, 2026');
  });

  it('bounds a long list and offers the rest', () => {
    const breaks = Array.from({ length: 30 }, (_, i) => ({ consumers: [`c-${i}`], reason: 'r' }));
    comp = mount(BreaksSection, { target, props: { open: true, services, breaks } });
    expect(target.querySelectorAll('[data-testid="break-row"]').length).toBe(25);
    expect(target.textContent).toContain('30');
  });
});

describe('InterfacesSection — port', () => {
  it('shows the port an interface declares', () => {
    comp = mount(InterfacesSection, { target, props: { interfaces: [{ name: 'api', type: 'openapi', port: 8080 }] } });
    expect(target.textContent).toContain(':8080');
  });
});

describe('DependenciesSection — minimal contract', () => {
  it('says an unknown range is not declared and marks a name from ref', () => {
    comp = mount(DependenciesSection, { target, props: { name: 's', services: [{ name: 's' }], dependencies: [
      { name: 'payments', ref: 'oci://r/payments', required: true, compatibility: 'unknown', compatibilityUnknown: true, nameFromRef: true },
    ] } });
    expect(target.textContent).toContain('Range not declared');
    expect(target.querySelector('[data-testid="dep-name-from-ref"]')?.textContent).toContain('name from ref');
  });
});
