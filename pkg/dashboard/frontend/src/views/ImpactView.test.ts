/**
 * Component render tests for ImpactView.svelte.
 * Verifies the analyze form, the classification badge, the breaking-changes and
 * affected-consumers tables, the empty-consumers state and the error state.
 * The `api` module is mocked so no network is hit.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const fleetImpactFn = vi.fn();

vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return {
    ...actual, // keep the real ApiError so `instanceof` still works
    api: { fleetImpact: (...a: unknown[]) => fleetImpactFn(...a) },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import ImpactView from './ImpactView.svelte';
import { ApiError } from '../lib/api.ts';

const result = {
  schemaVersion: 'pacto.dev/fleet/v1',
  snapshotId: 'abc',
  asOf: '2026-07-29T10:00:00Z',
  service: 'payments',
  oldVersion: '1.0.0',
  newVersion: '2.0.0',
  classification: 'BREAKING',
  completeness: 'complete',
  breakingChanges: [
    { path: 'paths./pay.post', type: 'removed', classification: 'BREAKING', reason: 'operation removed' },
  ],
  consumers: [
    { service: 'billing', depth: 1, direct: true, path: ['billing', 'payments'], owner: 'core', required: true, compatibilityVerdict: 'incompatible', provenance: 'declared', confidence: 'contractual', targets: ['prod/k8s/billing'] },
    { service: 'ledger', depth: 2, direct: false, path: ['ledger', 'billing', 'payments'], required: false, compatibilityVerdict: 'unknown', provenance: 'inferred', confidence: 'inferred' },
  ],
  owners: ['core'],
  activeTargets: ['prod/k8s/billing'],
};

function setInput(target: HTMLElement, sel: string, value: string) {
  const el = target.querySelector(sel) as HTMLInputElement;
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

function submit(target: HTMLElement) {
  const form = target.querySelector('form') as HTMLFormElement;
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

async function analyze(target: HTMLElement) {
  setInput(target, '#impact-old', 'oci://svc:1.0.0');
  setInput(target, '#impact-new', 'oci://svc:2.0.0');
  flushSync();
  submit(target);
}

describe('ImpactView — analyze form + results', () => {
  let target: HTMLElement;

  beforeEach(() => {
    fleetImpactFn.mockReset();
    fleetImpactFn.mockResolvedValue(result);
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  it('renders the analyze form with both ref inputs and the include-observed checkbox', () => {
    const component = mount(ImpactView, { target });
    expect(target.querySelector('#impact-old')).toBeTruthy();
    expect(target.querySelector('#impact-new')).toBeTruthy();
    expect(target.querySelector('input[type="checkbox"]')).toBeTruthy();
    const btn = target.querySelector('button[type="submit"]');
    expect(btn?.textContent).toContain('Analyze');
    unmount(component);
  });

  it('calls fleetImpact with the entered refs and observed flag on submit', async () => {
    const component = mount(ImpactView, { target });
    const cb = target.querySelector('input[type="checkbox"]') as HTMLInputElement;
    cb.checked = true;
    cb.dispatchEvent(new Event('change', { bubbles: true }));
    await analyze(target);
    await vi.waitFor(() => {
      expect(fleetImpactFn).toHaveBeenCalledWith('oci://svc:1.0.0', 'oci://svc:2.0.0', true);
    });
    unmount(component);
  });

  it('renders the classification badge and breaking changes', async () => {
    const component = mount(ImpactView, { target });
    await analyze(target);
    await vi.waitFor(() => {
      const badge = target.querySelector('.impact-summary .badge');
      expect(badge?.textContent).toContain('BREAKING');
      const text = target.textContent || '';
      expect(text).toContain('Breaking changes');
      expect(text).toContain('paths./pay.post');
      expect(text).toContain('operation removed');
    });
    unmount(component);
  });

  it('renders the affected-consumers table with reach, verdict, confidence and provenance', async () => {
    const component = mount(ImpactView, { target });
    await analyze(target);
    await vi.waitFor(() => {
      expect(target.querySelector('a[href="#/services/billing"]')).toBeTruthy();
      expect(target.querySelector('a[href="#/services/ledger"]')).toBeTruthy();
      const text = target.textContent || '';
      expect(text).toContain('Direct');
      expect(text).toContain('Transitive (depth 2)');
      expect(text).toContain('incompatible');
      expect(text).toContain('contractual');
      expect(text).toContain('declared');
    });
    unmount(component);
  });

  it('renders owners and active targets', async () => {
    const component = mount(ImpactView, { target });
    await analyze(target);
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Owners');
      expect(text).toContain('Active targets');
      expect(text).toContain('prod/k8s/billing');
    });
    unmount(component);
  });

  it('shows an empty state when there are no affected consumers', async () => {
    fleetImpactFn.mockResolvedValue({ ...result, consumers: [], breakingChanges: [] });
    const component = mount(ImpactView, { target });
    await analyze(target);
    await vi.waitFor(() => {
      expect(target.textContent || '').toContain('No affected consumers');
    });
    unmount(component);
  });

  it('renders an error state with retry when the analysis fails', async () => {
    fleetImpactFn.mockRejectedValue(new ApiError(422, 'invalid ref'));
    const component = mount(ImpactView, { target });
    await analyze(target);
    await vi.waitFor(() => {
      const text = target.textContent || '';
      expect(text).toContain('Couldn’t analyze the impact');
      expect(text).toContain('invalid ref');
    });
    unmount(component);
  });
});
