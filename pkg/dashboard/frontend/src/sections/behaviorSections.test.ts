/**
 * The offline single-service page (`pacto doc`) shows the contract features the
 * product pages show, through the same components: interface lifecycle, guarantees
 * and epoch, what each dependency requires and whether it was met, refused or not
 * verified, the validation stages and each environment's check.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import InterfacesSection from './InterfacesSection.svelte';
// @ts-expect-error
import DependenciesSection from './DependenciesSection.svelte';
// @ts-expect-error
import ValidationSection from './ValidationSection.svelte';
// @ts-expect-error
import EnvironmentsSection from './EnvironmentsSection.svelte';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const tags = () => [...target.querySelectorAll('.tag')].map((t) => t.textContent);

describe('InterfacesSection — behavior', () => {
  it('marks a deprecated interface and lists its guarantees and epoch', () => {
    comp = mount(InterfacesSection, { target, props: { interfaces: [
      { name: 'orders', type: 'asyncapi', lifecycle: { deprecated: '2020-01-01', sunset: '2020-06-01', successor: 'orders-v2' }, guarantees: { delivery: 'at-least-once' }, compatibilityEpoch: 3 },
      { name: 'plain', type: 'openapi' },
    ] } });
    expect(tags()).toEqual(['Past sunset', 'Epoch 3']);
    expect(target.querySelector('.iface-lifecycle')?.textContent).toContain('successor orders-v2');
    expect(target.querySelector('dl')?.getAttribute('aria-label')).toBe('Guarantees of orders');
    expect(target.querySelectorAll('.iface-behavior')).toHaveLength(1);
  });
});

describe('DependenciesSection — requirements', () => {
  const req = [{ interface: 'api', idempotent: true }];
  it('keeps met, refused and not verified apart', () => {
    comp = mount(DependenciesSection, { target, props: { name: 's', services: [{ name: 's' }], dependencies: [
      { name: 'met', ref: 'oci://r/met', required: true, requires: req, guaranteeStatus: 'met', deprecatedInterfaces: [{ interface: 'old', deprecated: '2020-01-01' }] },
      { name: 'refused', ref: 'oci://r/refused', required: true, requires: req, guaranteeStatus: 'unmet',
        unmet: [{ consumer: 's', provider: 'refused', interface: 'api', guarantee: 'idempotent', required: 'true' }] },
      { name: 'unknown', ref: 'oci://r/unknown', required: true, requires: req, guaranteeStatus: 'unknown', guaranteeReason: "the provider's contract was not fetched" },
      { name: 'plain', ref: 'oci://r/plain', required: false },
    ] } });
    const badges = [...target.querySelectorAll('[data-testid="dep-behavior"]')].map((b) => b.textContent?.trim());
    expect(badges).toEqual(['Guarantees met old: deprecated', 'Refused', 'Not verified']);
    const rows = [...target.querySelectorAll('.dep-requires-row')];
    expect(rows).toHaveLength(3);
    expect(rows[1].textContent).toContain('Refused');
    expect(rows[1].textContent).toContain('api: Idempotent yes required, nothing offered');
    expect(rows[2].textContent).toContain("Not verified: the provider's contract was not fetched.");
    expect(rows[2].textContent).not.toContain('Refused');
  });
});

describe('ValidationSection — stages', () => {
  it('shows how each stage ended even with no findings', () => {
    comp = mount(ValidationSection, { target, props: { open: true, validation: { valid: true }, stages: [
      { name: 'structural', status: 'pass' }, { name: 'policy', status: 'skip', reason: 'structural failed' },
    ] } });
    expect(tags()).toEqual(['Structure passed', 'Policy not run']);
    expect(target.textContent).not.toContain('None declared');
  });
  it('says None declared with no stages and no findings', () => {
    comp = mount(ValidationSection, { target, props: { open: true, validation: { valid: true } } });
    expect(target.textContent).toContain('None declared');
  });
});

describe('EnvironmentsSection', () => {
  const env = (name: string, status: string, extra = {}) => ({ name, file: `${name}.yaml`, status, findings: { total: 0, count: 0, truncated: false, items: [] }, ...extra });
  it('summarises what needs attention and lists each environment', () => {
    comp = mount(EnvironmentsSection, { target, props: { open: true, environments: [
      env('prod', 'fail', { findings: { total: 1, count: 1, truncated: false, items: [{ code: 'ENVIRONMENT_VALUES_INVALID', severity: 'error', path: 'environments[0]', message: 'bad', file: 'prod.yaml', line: 2, column: 3 }] } }),
      env('dev', 'unverified'), env('qa', 'skip', { reason: 'structural failed' }), env('staging', 'pass'),
    ] } });
    expect(target.querySelector('[data-testid="environments-summary"]')?.textContent).toBe('1 failed, 1 not verified and 1 not checked');
    expect(tags()).toEqual(['Failed', 'Error', 'Not verified', 'Not checked', 'Passed']);
    expect(target.textContent).toContain('prod.yaml:2:3');
    expect(target.textContent).toContain('Not checked: structural failed.');
  });
  it('says so when every environment passed, and caps thousands of rows', () => {
    const many = Array.from({ length: 3000 }, (_, i) => env(`e${i}`, 'pass'));
    comp = mount(EnvironmentsSection, { target, props: { open: true, environments: many } });
    expect(target.querySelector('[data-testid="environments-summary"]')?.textContent).toBe('Every environment passed');
    expect(target.querySelectorAll('[data-testid="environment"]')).toHaveLength(25);
    (target.querySelector('.show-all') as HTMLButtonElement).click();
    flushSync();
    expect(target.querySelectorAll('[data-testid="environment"]')).toHaveLength(3000);
  });
  it('says None declared when there are none', () => {
    comp = mount(EnvironmentsSection, { target, props: { open: true, environments: [] } });
    expect(target.textContent).toContain('None declared');
  });
});
