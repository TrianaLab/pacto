/**
 * How an approver was classified, what let them decide and, beside it, one
 * mark saying who signed the approval record: the person keyless, or the
 * recorder on their behalf.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ApprovalAccess from './ApprovalAccess.svelte';

const REC = 'sha256:' + 'a'.repeat(64);
let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

function render(a: Record<string, unknown>) {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(ApprovalAccess, { target, props: { a } });
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}

function tipOf(t: HTMLElement) {
  const btn = t.querySelector('button[aria-label="How this approval was signed"]') as HTMLButtonElement;
  expect(btn).not.toBeNull();
  btn.focus(); flushSync(); // keyboard reachable: focus opens the tip
  return (t.querySelector('.prov-tip') as HTMLElement).textContent;
}

describe('ApprovalAccess', () => {
  it('says a person signed keyless, with the record id', () => {
    const t = render({ by: 'alice@acme.com', decision: 'approve', record: REC, signer: { kind: 'person' } });
    expect(tipOf(t)).toBe(`Signed by alice@acme.com with a keyless certificate. Record ${REC}.`);
  });

  it('says the recorder signed for the person admission checked', () => {
    const t = render({ by: 'alice@acme.com', decision: 'approve', record: REC,
      signer: { kind: 'recorder', onBehalfOf: { user: 'alice@acme.com', verified: 'admission' } } });
    expect(tipOf(t)).toBe(`Signed by the recorder for alice@acme.com, whom the cluster's admission checked. Record ${REC}.`);
  });

  it('shows no mark for an approval with no signed record', () => {
    const t = render({ by: 'alice@acme.com', decision: 'approve', classifiedBy: 'groups' });
    expect(t.querySelector('button')).toBeNull();
  });
});
