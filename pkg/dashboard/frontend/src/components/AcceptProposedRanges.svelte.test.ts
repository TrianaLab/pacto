/**
 * The Inferred block's one action: the plan pacto accept would sign, with the exact
 * command a person runs; a refusal with its command; acceptances that do not count as
 * muted lines with a mark. The dashboard never signs or writes.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { acceptFn } = vi.hoisted(() => ({ acceptFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetAccept: (...a: unknown[]) => acceptFn(...a) } };
});
// @ts-expect-error — Svelte component has no declaration file
import AcceptProposedRanges from './AcceptProposedRanges.svelte';
import { ApiError } from '../lib/api.ts';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { acceptFn.mockReset(); target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();
const open = async () => {
  comp = mount(AcceptProposedRanges, { target, props: { serviceKey: 'acme/reports', service: 'reports' } });
  flushSync();
  (target.querySelector('button.apr-action') as HTMLButtonElement).click();
  await vi.waitFor(() => expect(target.querySelector('[data-testid="accept-plan"] p:not(.apr-note)') || target.querySelector('[role="alert"]')).toBeTruthy());
  flushSync();
};
const COMMAND = 'pacto accept oci://ghcr.io/acme/reports --proposed --intent sha256:abc';

describe('AcceptProposedRanges', () => {
  it('shows the plan and the copyable command, and plans for the service key', async () => {
    acceptFn.mockResolvedValue({ command: COMMAND, plan: { service: 'acme/reports', version: '1.6.0', subject: 'ghcr.io/acme/reports@sha256:1',
      ranges: [{ field: 'dependencies[ledger].compatibility', value: '^2.3.0', proposed: '^2.3.0', source: 'snapshot:ghcr.io/acme/s@sha256:2' }],
      whoMayAccept: 'ana@acme.com (the DRI of reports)', intentDigest: 'sha256:abc',
      existing: [{ state: 'routed', by: 'bob@acme.com', record: 'r@sha256:3', reason: 'waiting for the owner of reports' }, { state: 'counted', by: 'ana@acme.com' }] } });
    await open();
    expect(acceptFn).toHaveBeenCalledWith('acme/reports');
    const plan = target.querySelector('[data-testid="accept-plan"]')!;
    expect(text(plan)).toContain('dependencies[ledger].compatibility ^2.3.0 from snapshot:ghcr.io/acme/s@sha256:2');
    expect(text(plan)).toContain('ana@acme.com (the DRI of reports)');
    expect(plan.querySelector(`[title="${COMMAND}"]`)).toBeTruthy();
    expect(plan.querySelector('button[aria-label="Copy command"]')).toBeTruthy();
    // Only the routed record is a muted line; a counted one is not listed as waiting.
    const waiting = [...plan.querySelectorAll('[data-testid="accept-waiting"]')];
    expect(waiting).toHaveLength(1);
    expect(waiting[0].querySelector('button[aria-label="About routed acceptance"]')).toBeTruthy();
    expect(text(waiting[0])).toContain('Waiting for the owner: bob@acme.com');
  });

  it('shows a refusal with its command', async () => {
    acceptFn.mockResolvedValue({ refusal: { code: 'AcceptNothingProposed', message: 'nothing to accept', command: 'pacto ci --publish .' } });
    await open();
    expect(text(target.querySelector('[data-testid="accept-refusal"]'))).toBe('nothing to accept');
    expect(target.querySelector('[title="pacto ci --publish ."]')).toBeTruthy();
    expect(text(target.querySelector('summary'))).toContain('Nothing to accept');
  });

  it('says why a plan could not be read', async () => {
    acceptFn.mockRejectedValue(new ApiError(404, 'not found'));
    await open();
    expect(text(target.querySelector('[role="alert"]'))).toContain('pacto accept <bundle repository> --proposed --dry-run');
    acceptFn.mockReset();
    acceptFn.mockRejectedValue(new ApiError(403, 'sign in to plan an acceptance'));
    unmount(comp!); comp = null;
    await open();
    expect(text(target.querySelector('[role="alert"]'))).toBe('sign in to plan an acceptance');
  });
});
