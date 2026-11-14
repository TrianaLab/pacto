/**
 * The plan preview every propose flow shares: what the promoter would do, what
 * happens next, what must move first and what earlier changes like it did.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const plan = vi.hoisted(() => vi.fn());
const propose = vi.hoisted(() => vi.fn());
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, deliveryPlan: (...a: unknown[]) => plan(...a), deliveryPropose: (...a: unknown[]) => propose(...a) } };
});
import { ApiError } from '../lib/api.ts';
import { session } from '../lib/session.svelte.ts';

// @ts-expect-error — Svelte component has no declaration file
import ProposePanel from './ProposePanel.svelte';

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

const request = { namespace: 'prod', stage: 'pay', type: 'delivery.pacto.trianalab.io/promote', to: 'sha256:' + '2'.repeat(64) };

function render(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ProposePanel, { target, props: { request, toVersion: '2.0.0', ...props } });
  return { target, destroy: () => { unmount(c); target.remove(); } };
}

describe('ProposePanel', () => {
  beforeEach(() => {
    plan.mockReset();
    propose.mockReset();
    session.caller = null;
    location.hash = '';
  });

  it('renders next, order and precedent', async () => {
    plan.mockResolvedValue({
      type: request.type, predicted: 'Held', intentDigest: 'sha256:i',
      next: ['A person of team-pay approves it.', 'Then the promoter applies it.'],
      order: { moveFirst: [{ service: 'ledger', message: 'ledger must reach 2.1.0 in prod first' }], unknown: ['billing: its Stage cannot be read'] },
      precedent: { completeness: 'complete', entries: [{ record: 'r1', seq: 4, stream: 's', type: request.type, outcome: 'Succeeded', reason: 'routine bump', finished: '2026-11-01T10:00:00Z' }] },
    });
    const v = render();
    await settle();
    const next = v.target.querySelector('[data-testid="plan-next"]')!;
    expect(next.textContent).toContain('A person of team-pay approves it.');
    expect(next.textContent).toContain('Then the promoter applies it.');
    const order = v.target.querySelector('[data-testid="plan-order"]')!;
    expect(order.textContent).toContain('ledger must reach 2.1.0 in prod first');
    expect(order.textContent).toContain('billing: its Stage cannot be read');
    const prec = v.target.querySelector('[data-testid="plan-precedent"]')!;
    expect(prec.textContent).toContain('Succeeded');
    expect(prec.textContent).toContain('routine bump');
    v.destroy();
  });

  it('shows none of them when the plan has none, and a partial precedent says so', async () => {
    plan.mockResolvedValue({ type: request.type, predicted: 'Proceeds', intentDigest: 'sha256:i', precedent: { completeness: 'partial', note: 'the stream could not be read', entries: [] } });
    const v = render();
    await settle();
    expect(v.target.querySelector('[data-testid="plan-next"]')).toBeNull();
    expect(v.target.querySelector('[data-testid="plan-order"]')).toBeNull();
    const prec = v.target.querySelector('[data-testid="plan-precedent"]')!;
    expect(prec.textContent).toContain('The stream could not be read.');
    expect(prec.textContent).toContain('partial');
    v.destroy();
  });

  const fresh = { type: request.type, predicted: 'Proceeds', intentDigest: 'sha256:fresh' };
  const created = { operation: { metadata: { namespace: 'prod', name: 'pay-promote-1' } } };
  const stale = (differs: string) => new ApiError(409, 'moved', 'PlanStale', fresh as never, differs);

  async function opened(props: Record<string, unknown> = {}) {
    plan.mockResolvedValue({ type: request.type, predicted: 'Proceeds', intentDigest: 'sha256:read' });
    const v = render(props);
    await settle();
    return v;
  }
  function type(v: { target: HTMLElement }, sel: string, value: string) {
    const el = v.target.querySelector(sel) as HTMLInputElement;
    el.value = value;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
  }
  async function submit(v: { target: HTMLElement }) {
    v.target.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }));
    await settle();
  }

  it('re-proposes once when only the reason differs', async () => {
    propose.mockRejectedValueOnce(stale('reason')).mockResolvedValueOnce(created);
    const v = await opened();
    type(v, '.pp-form input[maxlength="500"]', 'CHG-1 hotfix');
    await submit(v);
    expect(propose).toHaveBeenCalledTimes(2);
    expect(propose.mock.calls[0][0]).toMatchObject({ reason: 'CHG-1 hotfix', intentDigest: 'sha256:read' });
    expect(propose.mock.calls[1][0]).toMatchObject({ reason: 'CHG-1 hotfix', intentDigest: 'sha256:fresh' });
    expect(v.target.querySelector('[role="alert"]')).toBeNull();
    expect(location.hash).toContain('pay-promote-1');
    v.destroy();
  });

  it('warns when the Stage moved', async () => {
    propose.mockRejectedValueOnce(stale(''));
    const v = await opened();
    await submit(v);
    expect(propose).toHaveBeenCalledTimes(1);
    expect(v.target.querySelector('[role="alert"]')?.textContent).toContain('Something changed since the plan');
    v.destroy();
  });

  it('never retries twice', async () => {
    propose.mockRejectedValue(stale('reason'));
    const v = await opened();
    type(v, '.pp-form input[maxlength="500"]', 'why');
    await submit(v);
    expect(propose).toHaveBeenCalledTimes(2);
    expect(v.target.querySelector('[role="alert"]')?.textContent).toContain('Something changed since the plan');
    v.destroy();
  });

  it('asks an agent who it acts for', async () => {
    let v = await opened();
    expect(v.target.querySelector('[data-testid="pp-on-behalf-of"]')).toBeNull();
    v.destroy();
    session.caller = { class: 'agent' } as never;
    v = await opened();
    const field = v.target.querySelector('[data-testid="pp-on-behalf-of"]') as HTMLInputElement;
    expect(field.required).toBe(true);
    expect(field.closest('label')!.textContent).toContain('Who asked you for this (on behalf of)');
    v.destroy();
  });

  it('loads the plan for an agent and sends onBehalfOf on propose', async () => {
    session.caller = { class: 'agent' } as never;
    propose.mockResolvedValue(created);
    const v = await opened();
    type(v, '[data-testid="pp-on-behalf-of"]', ' dana@example.com ');
    await submit(v);
    expect(plan.mock.calls[0][0]).not.toHaveProperty('onBehalfOf');
    expect(propose.mock.calls[0][0]).toMatchObject({ onBehalfOf: 'dana@example.com' });
    v.destroy();
  });

  it('sends the correlation id on propose only', async () => {
    propose.mockResolvedValue(created);
    const v = await opened({ correlation: 'INC-7' });
    const field = v.target.querySelector('[data-testid="pp-correlation"]') as HTMLInputElement;
    expect([field.value, field.maxLength, field.pattern, field.required]).toEqual(['INC-7', 128, '[ -~]*', false]);
    type(v, '[data-testid="pp-correlation"]', 'CHG-2041');
    await submit(v);
    expect(plan.mock.calls[0][0]).not.toHaveProperty('correlationID');
    expect(propose.mock.calls[0][0]).toMatchObject({ correlationID: 'CHG-2041' });
    expect(propose.mock.calls[0][0].onBehalfOf).toBeUndefined();
    v.destroy();
  });

  it('preview shows no correlation or on-behalf-of field', async () => {
    session.caller = { class: 'agent' } as never;
    const v = render({ preview: { type: request.type, predicted: 'Proceeds', intentDigest: 'sha256:i' } });
    await settle();
    expect(plan).not.toHaveBeenCalled();
    expect(v.target.querySelector('[data-testid="pp-correlation"]')).toBeNull();
    expect(v.target.querySelector('[data-testid="pp-on-behalf-of"]')).toBeNull();
    v.destroy();
  });

  it('shows NoPrincipal from a refused propose', async () => {
    session.caller = { class: 'agent' } as never;
    propose.mockRejectedValue(new ApiError(422, 'NoPrincipal: an agent must name the person it acts for', 'NoPrincipal'));
    const v = await opened();
    type(v, '[data-testid="pp-on-behalf-of"]', 'dana@example.com');
    await submit(v);
    expect(propose).toHaveBeenCalledTimes(1);
    expect(v.target.textContent).toContain('NoPrincipal: an agent must name the person it acts for');
    v.destroy();
  });
});
