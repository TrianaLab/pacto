/**
 * The plan preview every propose flow shares: what the promoter would do, what
 * happens next, what must move first and what earlier changes like it did.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const plan = vi.hoisted(() => vi.fn());
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, deliveryPlan: (...a: unknown[]) => plan(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import ProposePanel from './ProposePanel.svelte';

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

const request = { namespace: 'prod', stage: 'pay', type: 'delivery.pacto.trianalab.io/promote', to: 'sha256:' + '2'.repeat(64) };

function render() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ProposePanel, { target, props: { request, toVersion: '2.0.0' } });
  return { target, destroy: () => { unmount(c); target.remove(); } };
}

describe('ProposePanel', () => {
  beforeEach(() => { plan.mockReset(); });

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
});
