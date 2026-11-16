/**
 * StageOffers: a Stage's offers as buttons and one panel under the Stage it acts on.
 * Investigation pages show only what the caller may already take, matched to what the
 * page is about, and carry the investigation's correlation id into the panel.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const { plan, undo } = vi.hoisted(() => ({ plan: vi.fn(), undo: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, deliveryPlan: (...a: unknown[]) => plan(...a), deliveryUndo: (...a: unknown[]) => undo(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import StageOffers from './StageOffers.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const row = (over = {}) => ({ service: 'orders', namespace: 'prod', stage: 'orders', environment: 'prod', pointer: D1, candidate: D2, candidateVersion: '2.5.0', offers: [
  { action: 'promote', available: true, reason: 'promotes 2.5.0', to: D2 },
  { action: 'freeze', available: false, reason: 'an agent may not raise a freeze' },
  { action: 'undo', available: true, reason: 'undoes op-7', operation: 'op-7', to: D1 },
], ...over });

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}
function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(StageOffers, { target, props: { service: 'orders', ...props } });
  return { target, destroy: () => { unmount(c); target.remove(); } };
}
const buttons = (t: HTMLElement) => [...t.querySelectorAll<HTMLButtonElement>('[data-testid="service-delivery-offer"]')];
const words = (b: Element) => b.textContent?.replace(/\s+/g, ' ').trim();

describe('StageOffers', () => {
  beforeEach(() => { plan.mockReset(); undo.mockReset(); });

  it('shows only available offers', () => {
    const v = render({ rows: [row()], onlyAvailable: true, labelPrefix: 'Plan' });
    expect(buttons(v.target).map(words)).toEqual(['Plan promote of 2.5.0 in prod…', 'Plan undo in prod…']);
    expect(v.target.textContent).not.toContain('Freeze');
    v.destroy();
  });

  it('names the Stage when two Stages deliver one environment', () => {
    const rows = [row({ namespace: 'team-a', stage: 'payments' }), row({ namespace: 'team-b', stage: 'payments' }), row({ namespace: 'test', environment: 'test' })];
    const v = render({ rows, onlyAvailable: true, labelPrefix: 'Plan', match: (_r: unknown, o: { action: string }) => o.action === 'undo' });
    expect(buttons(v.target).map(words)).toEqual(['Plan undo in prod (team-a/payments)…', 'Plan undo in prod (team-b/payments)…', 'Plan undo in test…']);
    expect(buttons(v.target).map((b) => b.title)).toEqual(['Plan undo in prod (team-a/payments)…', 'Plan undo in prod (team-b/payments)…', 'Plan undo in test…']);
    v.destroy();
    const plain = render({ rows: rows.slice(0, 2), match: (_r: unknown, o: { action: string }) => o.action === 'undo' });
    expect(buttons(plain.target).map((b) => b.getAttribute('aria-label'))).toEqual(['Undo orders in prod (team-a/payments)', 'Undo orders in prod (team-b/payments)']);
    plain.destroy();
  });

  it('match filters offers', () => {
    const v = render({ rows: [row(), row({ namespace: 'test', environment: 'test' })], onlyAvailable: true, labelPrefix: 'Plan',
      match: (r: { namespace: string }, o: { action: string }) => o.action === 'undo' && r.namespace === 'test' });
    expect(buttons(v.target).map(words)).toEqual(['Plan undo in test…']);
    v.destroy();
  });

  it('opens one panel and returns focus', async () => {
    plan.mockResolvedValue({ type: 'delivery.pacto.trianalab.io/promote', predicted: 'Held', intentDigest: 'sha256:i', next: [] });
    undo.mockResolvedValue({ kind: 'none', note: 'nothing to undo' });
    const v = render({ rows: [row(), row({ namespace: 'test', environment: 'test' })], onlyAvailable: true, labelPrefix: 'Plan' });
    const [promote, , , undoTest] = buttons(v.target);
    promote.click();
    await settle();
    expect(plan).toHaveBeenCalledWith({ namespace: 'prod', stage: 'orders', type: 'delivery.pacto.trianalab.io/promote', to: D2, from: D1 });
    expect(v.target.querySelectorAll('[data-testid="propose-panel"]')).toHaveLength(1);
    undoTest.click();
    await settle();
    expect(v.target.querySelectorAll('[data-testid="propose-panel"]')).toHaveLength(0);
    expect(v.target.querySelectorAll('[data-testid="undo-panel"]')).toHaveLength(1);
    expect(undo).toHaveBeenCalledWith('test', 'op-7', 'orders');
    [...v.target.querySelectorAll<HTMLButtonElement>('[data-testid="undo-panel"] button')].find((b) => b.textContent === 'Close')!.click();
    await settle();
    expect(v.target.querySelector('[data-testid="undo-panel"]')).toBeNull();
    expect(document.activeElement).toBe(undoTest);
    v.destroy();
  });

  it('passes the correlation id to ProposePanel and UndoPanel', async () => {
    plan.mockResolvedValue({ type: 'delivery.pacto.trianalab.io/promote', predicted: 'Held', intentDigest: 'sha256:i', next: [] });
    undo.mockResolvedValue({ kind: 'promote', note: 'back to 2.4.0', command: 'pacto delivery propose', plan: { to: D1 }, versions: {} });
    const v = render({ rows: [row()], onlyAvailable: true, labelPrefix: 'Plan', correlation: 'CHG-1' });
    buttons(v.target)[0].click();
    await settle();
    expect((v.target.querySelector('[data-testid="pp-correlation"]') as HTMLInputElement).value).toBe('CHG-1');
    buttons(v.target)[1].click();
    await settle();
    [...v.target.querySelectorAll<HTMLButtonElement>('[data-testid="undo-panel"] button')].find((b) => /^Plan this/.test(b.textContent ?? ''))!.click();
    await settle();
    expect((v.target.querySelector('[data-testid="undo-panel"] [data-testid="pp-correlation"]') as HTMLInputElement).value).toBe('CHG-1');
    v.destroy();
  });

  it('gives each offer a title with its full label', () => {
    const v = render({ rows: [row()], onlyAvailable: true, labelPrefix: 'Plan' });
    expect(buttons(v.target).map((b) => b.title)).toEqual(['Plan promote of 2.5.0 in prod…', 'Plan undo in prod…']);
    expect([...buttons(v.target)[0].querySelectorAll('bdi')].map((b) => b.textContent)).toEqual(['2.5.0', 'prod']);
    v.destroy();
    const s = render({ rows: [row()] });
    expect(buttons(s.target).map((b) => [b.textContent, b.title, b.disabled])).toEqual([
      ['Promote', 'Promote orders in prod', false], ['Freeze', 'Freeze orders in prod', true], ['Undo', 'Undo orders in prod', false]]);
    s.destroy();
  });
});
