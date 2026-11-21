/** One next action for every list (513-S6): declare on an inferred service opens its
 *  Declare panel; anything else stays the words a person reads. */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import NextActionLink from './NextActionLink.svelte';

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });
function render(props: Record<string, unknown>) {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(NextActionLink, { target, props });
  flushSync();
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}
const DECLARE = { verb: 'declare', cli: 'pacto declare legacy-billing --namespace shop' };
const INFERRED = { kind: 'service', key: 'default/k8s:shop/Deployment/legacy-billing', label: 'legacy-billing', declaration: 'inferred' };

describe('NextActionLink', () => {
  it('links declare to the inferred service page with its panel open', () => {
    const a = render({ action: DECLARE, subject: INFERRED, cls: 'attn-action' }).querySelector('a') as HTMLAnchorElement;
    expect(a.textContent).toBe('Declare this service');
    expect(a.getAttribute('href')).toBe('#/fleet/services/default%2Fk8s%3Ashop%2FDeployment%2Flegacy-billing?declare=1');
    expect(a.getAttribute('title')).toBe(DECLARE.cli);
    expect(a.classList.contains('attn-action')).toBe(true);
  });

  it('keeps the words for other verbs and for a declare with no inferred service', () => {
    const edit = render({ action: { verb: 'edit-contract', cli: 'pacto ci' }, subject: INFERRED, cls: 'attn-action' });
    expect(edit.querySelector('a')).toBeNull();
    expect(edit.querySelector('span.attn-action')?.textContent).toBe('Fix: edit the contract, then run pacto ci');
    cleanup?.();
    const declared = render({ action: DECLARE, subject: { ...INFERRED, declaration: 'declared' } });
    expect(declared.querySelector('a')).toBeNull();
    expect(declared.textContent).toBe(`Fix: declare this service with ${DECLARE.cli}`);
    cleanup?.();
    expect(render({ action: null }).textContent).toBe('');
  });
});
