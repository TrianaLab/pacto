/**
 * The inbox's installation ladder block: the declared chain, then the muted inferred
 * leaves with their sources in the details, and [Confirm] only for a confirmer, which
 * hands off the exact command. The dashboard never signs.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import LadderBlock from './LadderBlock.svelte';

const DIGEST = `sha256:${'5be1'.repeat(16)}`;
const COMMAND = `pacto delivery ladder confirm --proposal ${DIGEST}`;
let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

function render(canConfirm: boolean) {
  const ladder = {
    declared: [{ name: 'staging', source: 'chart value delivery.environments.ladder', provenance: 'declared' }, { name: 'production', upstream: 'staging', source: 'chart value delivery.environments.ladder', provenance: 'declared' }],
    confirmed: [{ name: 'preview', source: 'namespaces *-preview, 4 found', provenance: 'confirmed' }],
    proposal: [{ name: 'perf', source: 'namespaces *-perf, 3 found', provenance: 'inferred' }],
    proposalDigest: DIGEST, canConfirm, command: COMMAND,
  };
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(LadderBlock, { target, props: { ladder } });
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}

describe('LadderBlock', () => {
  it('shows the declared chain then the muted inferred leaves, with sources in its collapsed details', () => {
    const t = render(true);
    const details = t.querySelector('[data-testid="inbox-ladder"]') as HTMLDetailsElement;
    expect(details.open).toBe(false);
    const line = (t.querySelector('summary') as HTMLElement).textContent!.replace(/\s+/g, ' ').trim();
    expect(line).toBe('Installation ladder: staging → production (from the chart) + preview + perf proposed');
    expect(t.querySelector('[data-testid="ladder-proposed"]')?.classList.contains('lb-inferred')).toBe(true);
    const rung = t.querySelector('[data-testid="ladder-rung"]') as HTMLElement;
    expect(rung.classList.contains('lb-inferred')).toBe(true);
    expect(rung.textContent).toBe('perf: inferred from namespaces *-perf, 3 found');
    expect(rung.querySelector('bdi')?.getAttribute('title')).toBe('perf');
  });

  it('renders Confirm only when canConfirm', () => {
    expect([...render(false).querySelectorAll('button')].some((b) => b.textContent === 'Confirm')).toBe(false);
    cleanup?.(); cleanup = null;
    const t = render(true);
    const btn = [...t.querySelectorAll('button')].find((b) => b.textContent === 'Confirm') as HTMLButtonElement;
    expect(t.querySelector('[data-testid="ladder-confirm"]')).toBeNull();
    btn.click(); flushSync();
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    expect(t.querySelector('.copyable-value')?.textContent).toBe(COMMAND);
  });

  it('copies the exact command', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const t = render(true);
    ([...t.querySelectorAll('button')].find((b) => b.textContent === 'Confirm') as HTMLButtonElement).click(); flushSync();
    (t.querySelector('button[aria-label="Copy command"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(COMMAND));
  });
});
