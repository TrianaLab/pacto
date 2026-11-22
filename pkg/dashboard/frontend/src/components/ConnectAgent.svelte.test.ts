import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ConnectAgent from './ConnectAgent.svelte';
import { session } from '../lib/session.svelte.ts';

const DEMO = 'This demo runs in your browser and serves no MCP endpoint. An installed Pacto serves it at /mcp.';
let target: HTMLElement;
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- component instance
let component: any;

function render(props: Record<string, unknown>) {
  component = mount(ConnectAgent, { target, props });
  flushSync();
  return target.querySelector('[data-testid="connect-agent"]') as HTMLDetailsElement | null;
}

beforeEach(() => { session.data = null; target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { unmount(component); target.remove(); });

describe('ConnectAgent', () => {
  it('shows the URL, the snippet and the token line when the host serves MCP', () => {
    const d = render({ mcp: { served: true, source: 'this dashboard, at /mcp' }, origin: 'https://pacto.example.com' });
    expect(d?.tagName).toBe('DETAILS');
    expect(d?.open).toBe(false);
    const summary = d!.querySelector('summary')!;
    expect(summary.textContent).toContain('Connect an agent');
    expect(summary.textContent).toContain('https://pacto.example.com/mcp');
    const pre = d!.querySelector('pre')!;
    expect(pre.getAttribute('tabindex')).toBe('0');
    expect(pre.textContent).toContain('"url": "https://pacto.example.com/mcp"');
    expect(pre.textContent).toContain('Bearer ${PACTO_TOKEN}');
    expect(d!.textContent).toContain('kubectl -n <namespace> create token pacto-viewer --audience pacto-dashboard');
    expect(d!.querySelector('a')?.getAttribute('href')).toBe('https://pacto.run/latest/mcp-integration/');
  });

  it('names the namespace of a pacto-viewer session in the token line', () => {
    session.data = { mode: 'shared', signedIn: true, user: 'system:serviceaccount:pacto:pacto-viewer' } as never;
    const d = render({ mcp: { served: true }, origin: 'http://127.0.0.1:3000' });
    expect(d!.textContent).toContain('kubectl -n pacto create token pacto-viewer');
  });

  it('says where an MCP server lives when the host serves none', () => {
    const d = render({ mcp: { served: false, source: DEMO }, origin: 'https://demo.example' });
    expect(d!.querySelector('summary')!.textContent?.trim()).toBe('Connect an agent');
    expect(d!.querySelector('[data-testid="connect-agent-source"]')?.textContent).toBe(DEMO);
    expect(d!.querySelector('pre')).toBeNull();
    expect(d!.querySelector('a')?.getAttribute('href')).toBe('https://pacto.run/latest/mcp-integration/');
  });

  it('renders nothing without installation.mcp', () => {
    expect(render({ mcp: undefined })).toBeNull();
    expect(target.textContent?.trim()).toBe('');
  });

  it('keeps a long origin inside <bdi> with the full URL as its title and copies exactly the URL', async () => {
    const origin = `https://${'a'.repeat(200 - 'https://'.length)}`;
    const url = `${origin}/mcp`;
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const d = render({ mcp: { served: true }, origin });
    const bdi = d!.querySelector('summary bdi')!;
    expect(bdi.textContent).toBe(url);
    expect(bdi.closest('[title]')?.getAttribute('title')).toBe(url);
    // No control inside the summary (nested interactive content): the copy control is in the body.
    expect(d!.querySelector('summary button')).toBeNull();
    (d!.querySelector('.ca-copy button') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(url));
  });
});
