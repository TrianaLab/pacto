/**
 * The decision on a held change: the success line says when the recorder signs
 * the decision into the registry, and the command a local dashboard offers
 * decides from the intent record when the change names one. `api` is mocked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const fns = vi.hoisted(() => ({ session: vi.fn(), decide: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  const { sessionApi } = await import('../testkit.svelte.ts');
  return { ...actual, api: { ...sessionApi(() => fns.session(), actual.ApiError), deliveryDecide: (...a: unknown[]) => fns.decide(...a) } };
});

import { loadSession } from '../lib/session.svelte.ts';
// @ts-expect-error — Svelte component has no declaration file
import DecisionPanel from './DecisionPanel.svelte';

const INTENT = 'registry.example/records@sha256:' + 'b'.repeat(64);

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

function op(status: Record<string, unknown> = {}) {
  return { metadata: { namespace: 'prod', name: 'op1' }, spec: { stage: 'pay' }, status: { phase: 'Held', plan: { floor: { decision: 'approve' } }, ...status } };
}

async function render(props: Record<string, unknown>) {
  loadSession();
  await settle();
  const target = document.createElement('div');
  document.body.appendChild(target);
  const decided = vi.fn();
  const c = mount(DecisionPanel, { target, props: { onDecided: decided, ...props } });
  await settle();
  return { target, decided, destroy: () => { unmount(c); target.remove(); } };
}

const shared = { mode: 'shared', login: true, signedIn: true, caller: { user: 'erin', groups: [] } };

describe('DecisionPanel', () => {
  beforeEach(() => { fns.session.mockReset(); fns.decide.mockReset(); sessionStorage.clear(); });

  it('says the recorder signs the decision when the server says so', async () => {
    fns.session.mockResolvedValue(shared);
    fns.decide.mockResolvedValue({ approval: 'op1-a1', signing: 'recorder' });
    const v = await render({ op: op(), actions: ['approve'] });
    (v.target.querySelector('[data-testid="decide-approve"]') as HTMLButtonElement).click();
    await settle();
    expect(v.decided).toHaveBeenCalledWith('Approve recorded as op1-a1. The recorder signs it for you into the registry, and the promoter acts once it is there.');
    v.destroy();
  });

  it('keeps the plain line when the Stage keeps no records', async () => {
    fns.session.mockResolvedValue(shared);
    fns.decide.mockResolvedValue({ approval: 'op1-a1' });
    const v = await render({ op: op(), actions: ['approve'] });
    (v.target.querySelector('[data-testid="decide-approve"]') as HTMLButtonElement).click();
    await settle();
    expect(v.decided).toHaveBeenCalledWith('Approve recorded as op1-a1. The promoter acts on it shortly.');
    v.destroy();
  });

  it('keeps the kubeconfig command on a local dashboard, intent record or not', async () => {
    // The --intent form needs --trust and --signers, which a copied line cannot carry.
    fns.session.mockResolvedValue({ mode: 'local', login: false, signedIn: true });
    for (const o of [op({ intentRecord: { digest: 'sha256:x', ref: INTENT } }), op()]) {
      const v = await render({ op: o });
      expect(v.target.textContent).toContain('pacto delivery approve -n prod op1 --decision approve');
      expect(v.target.textContent).not.toContain('--intent');
      v.destroy();
    }
  });
});
