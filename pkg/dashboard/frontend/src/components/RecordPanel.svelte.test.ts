/**
 * One record of a Stage's stream: the change it records, who asked and for
 * whom, who approved, every verdict and whether its signature was checked.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const record = vi.hoisted(() => vi.fn());
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { ...actual.api, deliveryRecord: (...a: unknown[]) => record(...a) } };
});

import { ApiError } from '../lib/api.ts';
// @ts-expect-error — Svelte component has no declaration file
import RecordPanel from './RecordPanel.svelte';

const D1 = 'sha256:' + '1'.repeat(64);
const D2 = 'sha256:' + '2'.repeat(64);
const REC = 'sha256:' + 'a'.repeat(64);

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}

function render(onClose = vi.fn()) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(RecordPanel, { target, props: { namespace: 'prod', stage: 'pay', id: REC, onClose } });
  return { target, onClose, destroy: () => { unmount(c); target.remove(); } };
}

const text = (el: Element | null) => el?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

describe('RecordPanel', () => {
  beforeEach(() => { record.mockReset(); });

  it('reads the record and says what it holds', async () => {
    record.mockResolvedValue({ namespace: 'prod', stage: 'pay', record: REC, verified: true, trust: 'sha256:t', asOf: '2026-11-11T10:00:00Z',
      entry: { operation: 'op1', type: 'delivery.pacto.trianalab.io/promote', from: D1, to: D2, actor: 'https://idp#carol', actorClass: 'human',
        finished: '2026-11-10T11:00:00Z', correlationID: 'rel-7', approvals: [{ by: 'https://idp#erin', decision: 'approve', note: 'ok' }] },
      onBehalfOf: 'https://idp#dan', intentReason: 'routine bump', note: 'read from the signed record in the registry, verified with trust config sha256:t',
      verdicts: [{ gate: 'pacto.diff', phase: 'pre', result: 'Pass' }, { gate: 'pacto.health', phase: 'verify', result: 'Fail', message: 'error rate 9%' }],
      versions: { [D1]: '1.0.0', [D2]: '2.0.0' }, people: { 'https://idp#carol': 'Carol Diaz' } });
    const v = render();
    expect(text(v.target.querySelector('[role="status"]'))).toBe('Reading the record…');
    await settle();
    expect(record).toHaveBeenCalledWith('prod', 'pay', REC);
    const p = v.target.querySelector('[data-testid="record-panel"]')!;
    expect(text(p.querySelector('h3'))).toBe('Record aaaaaaaaaaaa');
    expect(text(p.querySelector('[data-testid="record-change"]'))).toContain('Promote by Carol Diaz');
    expect(text(p.querySelector('[data-testid="record-change"]'))).toContain('from 1.0.0 to 2.0.0');
    expect(text(p.querySelector('[data-testid="record-change"]'))).toContain('Why: “routine bump”.');
    expect(text(p)).toContain('Correlation rel-7');
    expect(text(p)).toContain('Approve by');
    expect(text(p)).toContain(': “ok”');
    expect(p.querySelectorAll('[data-testid="record-verdicts"] > li').length).toBe(2);
    expect(text(p.querySelector('[data-testid="record-note"]'))).toBe('Read from the signed record in the registry, verified with trust config sha256:t.');
    (p.querySelector('button.btn') as HTMLButtonElement).click();
    expect(v.onClose).toHaveBeenCalled();
    v.destroy();
  });

  it('says when the signature was not checked and nobody approved', async () => {
    record.mockResolvedValue({ namespace: 'prod', stage: 'pay', record: REC, verified: false, asOf: '', note: 'read from the record in the registry; no trust config was given, so its signature was not checked',
      entry: { operation: 'op1', type: 'delivery.pacto.trianalab.io/rollback', actor: 'bot', actorClass: 'agent' }, verdicts: [] });
    const v = render();
    await settle();
    const p = v.target.querySelector('[data-testid="record-panel"]')!;
    expect(text(p)).toContain('No person approved it.');
    expect(text(p)).toContain('No checks yet.');
    expect(text(p.querySelector('[data-testid="record-note"]'))).toContain('so its signature was not checked.');
    p.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(v.onClose).toHaveBeenCalled();
    v.destroy();
  });

  it('shows a refusal', async () => {
    record.mockRejectedValue(new ApiError(404, 'record sha256:aaaa is not in the verified stream; see history', 'Not Found'));
    const v = render();
    await settle();
    expect(text(v.target)).toContain('is not in the verified stream; see history');
    v.destroy();
  });
});
