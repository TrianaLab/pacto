/**
 * The wave's one provenance mark: a button named by its caller, an "i" glyph and a tip
 * that opens from the keyboard and breaks a long object path.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ProvenanceMark from './ProvenanceMark.svelte';

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

const FROM = 'spec.template.spec.containers[name=cart].image';

function render() {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(ProvenanceMark, { target, props: { label: 'Inferred service: from k8s-workloads', text: `Inferred from k8s-workloads: ${FROM}` } });
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}

describe('ProvenanceMark', () => {
  it('is a button with the i glyph and the caller-composed accessible name', () => {
    const t = render();
    const btn = t.querySelector('button') as HTMLButtonElement;
    expect(btn.getAttribute('type')).toBe('button');
    expect(btn.textContent).toBe('i');
    expect(btn.getAttribute('aria-label')).toBe('Inferred service: from k8s-workloads');
  });

  it('opens its tip from the keyboard and keeps the path in a breakable element', () => {
    const t = render();
    const btn = t.querySelector('button') as HTMLButtonElement;
    const tip = t.querySelector('.prov-tip') as HTMLElement;
    expect(tip.hidden).toBe(true);
    btn.focus(); flushSync();
    expect(tip.hidden).toBe(false);
    expect(tip.textContent).toBe(`Inferred from k8s-workloads: ${FROM}`);
    btn.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); flushSync();
    expect(tip.hidden).toBe(true);
  });

  it('opens its tip toward the side with more room, never wider than that room', () => {
    const t = render();
    flushSync();
    const mark = t.querySelector('.prov-mark') as HTMLElement;
    const at = (left: number, right: number) => {
      mark.getBoundingClientRect = () => ({ left, right, top: 0, bottom: 18, width: right - left, height: 18, x: left, y: 0, toJSON() {} });
      (t.querySelector('button') as HTMLButtonElement).dispatchEvent(new FocusEvent('focusin', { bubbles: true })); flushSync();
    };
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    at(72, 90); // the end of a short wrapped line near the left edge
    expect(mark.classList.contains('prov-right')).toBe(true);
    expect(mark.style.getPropertyValue('--prov-room')).toBe('306px');
    at(350, 368); // the end of a full line
    expect(mark.classList.contains('prov-right')).toBe(false);
    expect(mark.style.getPropertyValue('--prov-room')).toBe('356px');
  });
});
