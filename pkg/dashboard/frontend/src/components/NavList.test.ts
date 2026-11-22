/**
 * The Attention badge counts what needs action: items above info severity, so the
 * knowledge gaps (info) never inflate it. An older engine without `actionable` falls
 * back to the full total.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { navGroups, attentionBadge } from '../lib/nav';

// @ts-expect-error — Svelte component has no declaration file
import NavList from './NavList.svelte';

let target: HTMLElement;
let component: ReturnType<typeof mount> | null = null;
function badgeText(attention: { actionable?: number; total?: number }): string | null | undefined {
  target = document.createElement('div');
  document.body.appendChild(target);
  component = mount(NavList, { target, props: { groups: navGroups({ fleet: true }), view: 'fleet-services', attentionTotal: attentionBadge(attention) } });
  flushSync();
  const att = target.querySelector('a[data-nav="overview"]') as HTMLElement;
  return document.getElementById(att.getAttribute('aria-describedby')!)?.textContent;
}

afterEach(() => {
  if (component) unmount(component);
  component = null;
  target?.remove();
});

describe('NavList — attention badge', () => {
  it('shows the actionable items, not the info-grade ones', () => {
    expect(badgeText({ actionable: 3, total: 40 })).toBe('3 items to look at');
  });

  it('falls back to the total when the engine sends no actionable count', () => {
    expect(badgeText({ total: 7 })).toBe('7 items to look at');
  });

  it('is unknown when the overview carries no attention preview', () => {
    expect(attentionBadge(null)).toBeNull();
  });
});
