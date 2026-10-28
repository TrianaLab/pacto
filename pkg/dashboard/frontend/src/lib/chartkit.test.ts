import { describe, it, expect, vi } from 'vitest';
import * as d3 from 'd3';
import { resolvePalette, prefersReducedMotion, emptyState, sharedTooltip, bindHover, legendItem } from './chartkit';

describe('chartkit', () => {
  it('resolvePalette returns token values with fallbacks', () => {
    const el = document.createElement('div');
    el.style.setProperty('--c-ok', '#34d399');
    el.style.setProperty('--c-text', '#f1f5f9');
    document.body.appendChild(el);
    const pal = resolvePalette(el);
    expect(pal.ok).toBeTruthy();
    expect(pal.text).toBeTruthy();
    document.body.removeChild(el);
  });
  it('emptyState renders a message box', () => {
    const el = document.createElement('div');
    emptyState(el, 'No data');
    expect(el.textContent).toContain('No data');
    expect(el.querySelector('.state-box')).not.toBeNull();
  });
  it('prefersReducedMotion respects matchMedia', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q }));
    expect(prefersReducedMotion()).toBe(true);
    vi.unstubAllGlobals();
  });
  it('sharedTooltip attaches and shows without throwing', () => {
    const el = document.createElement('div');
    const t = sharedTooltip(); t.attach(el); t.show('hi', 10, 10); t.hide();
    expect(el.querySelector('div')).not.toBeNull();
  });
  it('a moved tooltip that was hidden stays hidden', () => {
    // Regression: the heatmap re-showed the last cell's text on a mousemove over an
    // empty cell, because its mousemove re-showed whatever text the hidden tooltip held.
    const el = document.createElement('div');
    const t = sharedTooltip(); t.attach(el);
    const tip = el.querySelector('div') as HTMLDivElement;
    t.show('owner · security · 80%', 10, 10);
    t.move(20, 20);
    expect(tip.style.opacity).toBe('1');
    expect(tip.style.left).toBe('0px'); // clamped: a detached container has no width
    t.hide();
    t.move(30, 30);
    expect(tip.style.opacity).toBe('0');
  });
  it('bindHover shows content on enter, follows on move, hides on leave and says nothing for null', () => {
    const el = document.createElement('div');
    const t = sharedTooltip(); t.attach(el);
    const tip = el.querySelector('div') as HTMLDivElement;
    const svg = d3.select(el).append('svg');
    const marks = svg.selectAll('rect').data([{ v: 'a' }, { v: null }]).join('rect');
    bindHover(marks, t, (d) => d.v);
    const [withText, empty] = marks.nodes() as SVGRectElement[];
    withText.dispatchEvent(new MouseEvent('mouseenter'));
    expect(tip.textContent).toBe('a');
    expect(tip.style.opacity).toBe('1');
    withText.dispatchEvent(new MouseEvent('mousemove'));
    expect(tip.style.opacity).toBe('1');
    withText.dispatchEvent(new MouseEvent('mouseleave'));
    expect(tip.style.opacity).toBe('0');
    empty.dispatchEvent(new MouseEvent('mouseenter'));
    empty.dispatchEvent(new MouseEvent('mousemove'));
    expect(tip.style.opacity).toBe('0');
  });
  it('legendItem draws a dot and its label', () => {
    const svg = d3.select(document.createElement('div')).append('svg');
    const g = svg.append('g');
    legendItem(g, 12, 6, 'Breaking', 'red');
    const item = g.select('g');
    expect(item.attr('transform')).toBe('translate(12, 6)');
    expect(item.select('circle').attr('fill')).toBe('red');
    expect(item.select('text').text()).toBe('Breaking');
  });
});
