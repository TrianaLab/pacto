/**
 * The Members block on the Environments board of one service (514-S6): per
 * environment, in the order the server gives (the promotion graph), the members
 * a promote is placed on, where that placement came from and what each member
 * answered. Collapsed unless a member needs a look. The dashboard never signs.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import MemberPlacement from './MemberPlacement.svelte';

const EU = '9f1c4e2a-0000-4000-8000-000000000001';
const US = '7a2b3c4d-0000-4000-8000-000000000002';
const D = `sha256:${'5e1f'.repeat(16)}`;
const POLICY = `sha256:${'a1b2'.repeat(16)}`;
const NEXT = `pacto delivery propose --service billing --environment production --to ${D} --registry zot.test/acme --confirm`;

type Member = { uid: string; name: string; state: string; digest?: string; reason?: string; next?: string; asOf?: string; stale?: boolean };
const member = (over: Partial<Member>): Member => ({ uid: EU, name: 'eu-prod', state: 'applied', digest: D, ...over });
const plan = (environment: string, members: Member[], over: Record<string, unknown> = {}) => ({
  service: { name: 'billing' }, environment, members, hidden: 0, completeness: 'complete', asOf: '2026-11-24T10:00:00Z',
  placement: { source: 'policy', policy: POLICY, clusters: members.map((m) => ({ uid: m.uid, name: m.name, evidence: 'placement rule' })) },
  ...over,
});

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

function render(plans: unknown[]) {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(MemberPlacement, { target, props: { plans, service: 'billing' } });
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}
const block = (t: HTMLElement) => t.querySelector('[data-testid="member-placement"]') as HTMLDetailsElement;
// The text a reader sees: a closed tip stays in the DOM, hidden.
const text = (el: Element) => {
  const c = el.cloneNode(true) as Element;
  c.querySelectorAll('[hidden]').forEach((h) => h.remove());
  return c.textContent?.replace(/\s+/g, ' ').trim() ?? '';
};

describe('MemberPlacement', () => {
  it('renders a policy placement with its mark and tooltip', () => {
    const t = render([plan('production', [member({}), member({ uid: US, name: 'us-prod' })])]);
    const mark = t.querySelector('[data-testid="member-placement-source"] .help-btn') as HTMLElement;
    expect(mark.getAttribute('aria-label')).toBe('Where billing runs in production across members');
    const tip = t.querySelector('[data-testid="member-placement-source"] .prov-tip') as HTMLElement;
    expect(tip.textContent).toContain('From a signed placement rule');
    expect(tip.textContent).toContain(POLICY);
    expect(text(block(t).querySelector('summary')!)).toBe('Members: 2 members in 1 environment');
  });

  it('stays collapsed when every member applied', () => {
    expect(block(render([plan('production', [member({}), member({ uid: US, name: 'us-prod' })])])).open).toBe(false);
  });

  it('opens when a member is held and shows the approve command', () => {
    const approve = 'pacto delivery approve --intent sha256:abc --registry zot.test/acme';
    const t = render([plan('production', [member({ state: 'held', reason: 'production holds every promote for a person', next: approve })])]);
    expect(block(t).open).toBe(true);
    expect(text(block(t).querySelector('summary')!)).toBe('Members: 1 member in 1 environment, 1 needs a look');
    const detail = t.querySelector('[data-testid="member-detail"]') as HTMLElement;
    expect(text(detail)).toContain('Held for a person: production holds every promote for a person.');
    expect(detail.querySelector('.copyable-value')?.textContent).toBe(approve);
  });

  it("shows a refused member's reason inline", () => {
    const t = render([plan('production', [member({ state: 'refused', reason: 'alice may not create operations in billing', next: 'kubectl create rolebinding ...' })])]);
    expect(block(t).open).toBe(true);
    const detail = t.querySelector('[data-testid="member-detail"]') as HTMLElement;
    expect(text(detail)).toContain('Refused: alice may not create operations in billing.');
    expect(detail.querySelector('.copyable-value')?.textContent).toBe('kubectl create rolebinding ...');
  });

  it('mutes a stale member and names its asOf', () => {
    const t = render([plan('production', [member({ stale: true, asOf: '2026-11-23T08:00:00Z' })])]);
    expect(block(t).open).toBe(true);
    const li = t.querySelector('[data-testid="member"]') as HTMLElement;
    expect(li.classList.contains('mp-muted')).toBe(true);
    expect(text(li)).toContain('as of 2026-11-23 08:00 UTC');
  });

  it('mutes an unknown member', () => {
    const t = render([plan('production', [member({ state: 'unknown', asOf: '2026-11-23T08:00:00Z' })])]);
    expect(block(t).open).toBe(true);
    expect((t.querySelector('[data-testid="member"]') as HTMLElement).classList.contains('mp-muted')).toBe(true);
  });

  it('never shows a uid in a label', () => {
    const t = render([plan('production', [member({ state: 'refused', reason: 'r' })], { hidden: 1, hiddenReason: 'no live channel to it' })]);
    expect(text(t)).not.toContain(EU);
    for (const el of t.querySelectorAll('[aria-label]')) expect(el.getAttribute('aria-label')).not.toContain(EU);
    // The uid is in the tooltip of the member's mark only.
    expect((t.querySelector('a.mp-mark') as HTMLElement).getAttribute('title')).toContain(EU);
    expect(text(t)).toContain('1 member hidden: no live channel to it.');
  });

  it('mark and glyph are sibling controls, never nested', () => {
    const t = render([plan('production', [member({})])]);
    const a = t.querySelector('a.mp-mark') as HTMLElement;
    const b = t.querySelector('button.mp-glyph') as HTMLButtonElement;
    expect(a.parentElement).toBe(b.parentElement);
    expect(a.querySelector('button, a')).toBeNull();
    expect(b.querySelector('button, a')).toBeNull();
    expect(a.getAttribute('href')).toContain(encodeURIComponent(EU));
    expect(b.getAttribute('aria-label')).toBe('eu-prod: Applied');
    // The glyph opens the problem detail.
    expect(t.querySelector('[data-testid="member-detail"]')).toBeNull();
    b.click(); flushSync();
    expect(b.getAttribute('aria-expanded')).toBe('true');
    expect(t.querySelector('[data-testid="member-detail"]')).toBeTruthy();
  });

  it('rows follow the promotion graph, not names', () => {
    const t = render([plan('staging', [member({})]), plan('production', [member({})]), plan('canary', [member({})])]);
    expect([...t.querySelectorAll('[data-testid="member-env"]')].map((e) => e.getAttribute('data-environment'))).toEqual(['staging', 'production', 'canary']);
  });

  it('hands off the confirming command, never signs', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const t = render([plan('production', [member({ state: 'no request' })], { placement: { source: 'unconfirmed', clusters: [{ uid: EU, name: 'eu-prod', evidence: 'takes requests for production' }] }, next: NEXT })]);
    const tip = t.querySelector('[data-testid="member-placement-source"] .prov-tip') as HTMLElement;
    expect(tip.textContent).toContain('Proposed from advertised environments');
    const btn = [...t.querySelectorAll('button')].find((b) => b.textContent === 'Confirm placement') as HTMLButtonElement;
    btn.click(); flushSync();
    (t.querySelector('button[aria-label="Copy command"]') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(NEXT));
  });

  it('copy names no agent and never says impossible or proves', () => {
    const t = render([
      plan('production', [member({ state: 'held', reason: 'r', next: 'n' }), member({ uid: US, name: 'us-prod', state: 'waiting' })],
        { completeness: 'partial', notes: ['us-prod: snapshot is stale'], hidden: 2, hiddenReason: 'no live channel to them', next: NEXT }),
    ]);
    ([...t.querySelectorAll('button')].find((b) => b.textContent === 'Confirm placement') as HTMLButtonElement).click(); flushSync();
    const all = t.textContent ?? '';
    expect(all).not.toMatch(/\bagents?\b|impossible|proves/i);
    expect(text(t)).toContain('Read in part: us-prod: snapshot is stale.');
  });
});
