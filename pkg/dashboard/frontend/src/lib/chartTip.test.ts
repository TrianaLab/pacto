import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { get } from 'svelte/store';
import { mount, unmount, flushSync } from 'svelte';
import { chartTip, tipState, tipLabel, hideTip, TIP_ID, type TipParam } from './chartTip.ts';
import ChartTip from '../components/ChartTip.svelte';

const MODEL: TipParam = {
  title: 'Week of 3 Nov',
  rows: [
    { value: '4', label: 'breaking changes caught', color: 'var(--chart-1)' },
    { value: 'unknown', label: 'consumers protected', texture: 'hatch' },
  ],
  foot: 'counted once per change in each week it ran',
};

const pointer = (type: string, pointerType = 'mouse') => Object.assign(new MouseEvent(type, { clientX: 40, clientY: 90, bubbles: true }), { pointerType });

describe('chartTip', () => {
  let node: HTMLElement;
  let act: ReturnType<typeof chartTip>;
  beforeEach(() => {
    node = document.createElement('div');
    document.body.appendChild(node);
  });
  afterEach(() => {
    act?.destroy?.();
    node.remove();
    hideTip();
  });

  it('names an SVG mark with its title, rows and foot, and makes it a tab stop', () => {
    act = chartTip(node, MODEL);
    expect(node.getAttribute('role')).toBe('img');
    expect(node.getAttribute('tabindex')).toBe('0');
    expect(node.getAttribute('aria-label')).toBe('Week of 3 Nov, breaking changes caught 4, consumers protected unknown, counted once per change in each week it ran');
    expect(tipLabel({ title: 't', rows: [] })).toBe('t');
  });

  it('shows on focus and hides on blur and Escape', () => {
    act = chartTip(node, MODEL);
    node.dispatchEvent(new FocusEvent('focus'));
    expect(get(tipState)?.model.title).toBe('Week of 3 Nov');
    node.dispatchEvent(new FocusEvent('blur'));
    expect(get(tipState)).toBeNull();
    node.dispatchEvent(new FocusEvent('focus'));
    node.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(get(tipState)).toBeNull();
  });

  it('follows a mouse pointer and hides when it leaves', () => {
    act = chartTip(node, MODEL);
    node.dispatchEvent(pointer('pointerenter'));
    expect(get(tipState)).toMatchObject({ x: 40, y: 90 });
    node.dispatchEvent(pointer('pointerleave'));
    expect(get(tipState)).toBeNull();
  });

  it('toggles on a tap with a coarse pointer', () => {
    act = chartTip(node, MODEL);
    node.dispatchEvent(pointer('pointerenter', 'touch'));
    expect(get(tipState)).toBeNull();
    node.dispatchEvent(pointer('pointerup', 'touch'));
    expect(get(tipState)?.owner).toBe(node);
    node.dispatchEvent(pointer('pointerleave', 'touch'));
    expect(get(tipState)?.owner).toBe(node);
    node.dispatchEvent(pointer('pointerup', 'touch'));
    expect(get(tipState)).toBeNull();
  });

  it('keeps a table cell its own role: no role, no tabindex, described by the tooltip', () => {
    act = chartTip(node, { ...MODEL, role: false });
    expect(node.hasAttribute('role')).toBe(false);
    expect(node.hasAttribute('tabindex')).toBe(false);
    expect(node.hasAttribute('aria-label')).toBe(false);
    expect(node.getAttribute('aria-describedby')).toBe(TIP_ID);
  });

  it('refreshes a shown tooltip on update and hides it on destroy', () => {
    act = chartTip(node, MODEL);
    node.dispatchEvent(new FocusEvent('focus'));
    act!.update!({ ...MODEL, title: 'Week of 10 Nov' });
    expect(get(tipState)?.model.title).toBe('Week of 10 Nov');
    expect(node.getAttribute('aria-label')).toMatch(/^Week of 10 Nov/);
    act!.destroy!();
    expect(get(tipState)).toBeNull();
  });
});

describe('ChartTip', () => {
  let target: HTMLElement;
  let c: ReturnType<typeof mount>;
  let node: HTMLElement;
  let act: ReturnType<typeof chartTip>;
  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
    c = mount(ChartTip, { target });
    node = document.createElement('button');
    document.body.appendChild(node);
    act = chartTip(node, MODEL);
  });
  afterEach(() => {
    act?.destroy?.();
    unmount(c);
    target.remove();
    node.remove();
  });
  const tip = () => document.getElementById(TIP_ID) as HTMLElement;

  it('renders the value before the label, with line and texture keys', () => {
    node.dispatchEvent(new FocusEvent('focus'));
    flushSync();
    expect(tip().getAttribute('role')).toBe('tooltip');
    expect(tip().classList.contains('on')).toBe(true);
    expect(tip().querySelector('.tt')?.textContent).toBe('Week of 3 Nov');
    const rows = [...tip().querySelectorAll('.row')];
    expect(rows.map((r) => [r.querySelector('b')?.textContent, r.querySelector('b + span')?.textContent])).toEqual([
      ['4', 'breaking changes caught'], ['unknown', 'consumers protected'],
    ]);
    expect(rows[0].querySelector('.lk')?.getAttribute('style')).toMatch(/--chart-1/);
    expect(rows[1].querySelector('.lk.hatch')).not.toBeNull();
    expect(tip().querySelector('.foot')?.textContent).toBe(MODEL.foot);
  });

  it('prints markup in a label as text', () => {
    act!.update!({ title: '<img src=x onerror=alert(1)>', rows: [] });
    node.dispatchEvent(new FocusEvent('focus'));
    flushSync();
    expect(tip().querySelector('img')).toBeNull();
    expect(tip().querySelector('.tt')?.textContent).toBe('<img src=x onerror=alert(1)>');
  });

  it('hides on Escape anywhere and on a tap outside the mark', () => {
    node.dispatchEvent(new FocusEvent('focus'));
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    flushSync();
    expect(tip().classList.contains('on')).toBe(false);
    node.dispatchEvent(pointer('pointerup', 'touch'));
    document.body.dispatchEvent(pointer('pointerdown', 'touch'));
    flushSync();
    expect(get(tipState)).toBeNull();
  });

  it('stays inside the viewport', () => {
    tipState.set({ model: MODEL, x: innerWidth + 400, y: 2, owner: node });
    flushSync();
    expect(parseFloat(tip().style.left)).toBeLessThanOrEqual(innerWidth - 8);
    expect(parseFloat(tip().style.top)).toBeGreaterThanOrEqual(8);
  });
});
