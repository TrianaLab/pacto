/**
 * The environments board's Applier line: the values file the applier selects,
 * on the warn and the normal branch, with its provenance in a mark's tip.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const fns = vi.hoisted(() => ({ session: vi.fn(), environments: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  const { sessionApi } = await import('../testkit.svelte.ts');
  return { ...actual, api: { ...sessionApi(() => fns.session(), actual.ApiError), deliveryEnvironments: (...a: unknown[]) => fns.environments(...a) } };
});

import { loadSession } from '../lib/session.svelte.ts';
// @ts-expect-error — Svelte component has no declaration file
import DeliveryEnvironmentsView from './DeliveryEnvironmentsView.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const row = (applier: Record<string, unknown>) => ({ service: 'payments', namespace: 'production', stage: 'payments', environment: 'production', pointer: D1,
  version: '1.5.0', verified: true, failed: false, frozen: false, autoHold: false, held: 0, ready: 'True', actions: [], offers: [], applier });

// The line as read: the provenance mark's glyph and folded tip left out.
const said = (line: HTMLElement) => {
  const c = line.cloneNode(true) as HTMLElement;
  c.querySelectorAll('.prov-mark').forEach((m) => m.remove());
  return c.textContent?.replace(/\s+/g, ' ').trim();
};
const tip = (line: HTMLElement) => line.querySelector('.prov-mark .prov-tip')?.textContent;

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

async function applierOf(applier: Record<string, unknown>) {
  fns.environments.mockResolvedValue({ asOf: '2026-11-15T10:00:00Z', completeness: 'complete', environments: [row(applier)] });
  loadSession();
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(DeliveryEnvironmentsView, { target, props: {} });
  await settle();
  const line = target.querySelector('[data-testid="env-applier"]') as HTMLElement;
  return { line, destroy: () => { unmount(c); target.remove(); } };
}

describe('DeliveryEnvironmentsView applier values', () => {
  beforeEach(() => {
    fns.session.mockResolvedValue({ mode: 'local', login: false, signedIn: false });
  });

  it('normal branch: prints the values file with where it was observed in its mark', async () => {
    const { line, destroy } = await applierOf({ state: 'Adopted', display: 'Argo CD (Application payments)', values: 'values-production.yaml', asOf: '2026-11-15T09:59:30Z' });
    expect(line.classList.contains('ev-applier')).toBe(true);
    expect(said(line)).toBe('Applier: Argo CD (Application payments), values values-production.yaml (as of 2026-11-15 09:59 UTC)');
    expect(line.querySelectorAll('bdi')[1].textContent).toBe('values-production.yaml');
    expect(tip(line)).toBe('observed on Argo CD (Application payments) as of 2026-11-15 09:59 UTC');
    expect(line.querySelector('.prov-mark button')?.getAttribute('aria-label')).toBe('Where Pacto read the values file');
    destroy();
  });

  it('warn branch: prints the values file after the problem and in the span title, with its mark', async () => {
    const { line, destroy } = await applierOf({ state: 'Unknown', display: 'Flux (Kustomization payments)', message: 'cannot tell', values: 'overlays/production' });
    expect(line.classList.contains('ev-ready')).toBe(true);
    expect(said(line)).toBe('Applier: Flux (Kustomization payments), unknown: cannot tell, values overlays/production');
    expect(line.getAttribute('title')).toBe(said(line));
    expect(tip(line)).toBe('observed on Flux (Kustomization payments)');
    destroy();
  });

  it('prints no values clause when the applier selects none', async () => {
    const { line, destroy } = await applierOf({ state: 'Adopted', display: 'Argo CD (Application payments)' });
    expect(line.textContent).not.toContain('values');
    expect(line.querySelector('.prov-mark')).toBeNull();
    destroy();
  });
});
