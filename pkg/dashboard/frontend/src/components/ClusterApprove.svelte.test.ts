/**
 * [Approve] on a pending member hands the person the exact command: the key to check
 * and an optional display name, rebuilt as they type. The dashboard never signs.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ClusterApprove from './ClusterApprove.svelte';

const KEY = '91ab'.repeat(16);
let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

function render() {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(ClusterApprove, { target, props: { uid: '9f1c4e2a', keyID: KEY, name: 'eu-prod', registry: 'ghcr.io/acme/pacto-targets' } });
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}

const command = (t: HTMLElement) => t.querySelector('.copyable-value')?.textContent;

describe('ClusterApprove', () => {
  it('opens a panel with the full key, the command and what approving means', () => {
    const t = render();
    expect(t.querySelector('[data-testid="cluster-approve-panel"]')).toBeNull();
    const btn = t.querySelector('button.ca-btn') as HTMLButtonElement;
    btn.click(); flushSync();
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    expect(t.querySelector('.ca-key bdi')?.getAttribute('title')).toBe(`sha256:${KEY}`);
    expect(command(t)).toBe(`pacto cluster approve 9f1c4e2a --key sha256:${KEY} --registry ghcr.io/acme/pacto-targets`);
    expect(t.textContent).toContain('Approving signs a record as you.');
  });

  it('rebuilds the command from the name field and flags a name that is not a DNS label', () => {
    const t = render();
    (t.querySelector('button.ca-btn') as HTMLButtonElement).click(); flushSync();
    const input = t.querySelector('input') as HTMLInputElement;
    input.value = 'eu-prod-2'; input.dispatchEvent(new Event('input')); flushSync();
    expect(command(t)).toBe(`pacto cluster approve 9f1c4e2a --key sha256:${KEY} --name eu-prod-2 --registry ghcr.io/acme/pacto-targets`);
    expect(t.querySelector('.ca-bad')).toBeNull();
    input.value = 'EU Prod'; input.dispatchEvent(new Event('input')); flushSync();
    expect(command(t)).toBe(`pacto cluster approve 9f1c4e2a --key sha256:${KEY} --name 'EU Prod' --registry ghcr.io/acme/pacto-targets`);
    expect(t.querySelector('.ca-bad')?.textContent).toContain('DNS label');
    expect(input.getAttribute('aria-invalid')).toBe('true');
  });

  it('copies the command', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const t = render();
    (t.querySelector('button.ca-btn') as HTMLButtonElement).click(); flushSync();
    (t.querySelector('button[aria-label="Copy command"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(`pacto cluster approve 9f1c4e2a --key sha256:${KEY} --registry ghcr.io/acme/pacto-targets`));
  });
});
