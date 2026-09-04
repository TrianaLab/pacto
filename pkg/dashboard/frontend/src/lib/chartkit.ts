/**
 * Shared chart primitives for D3 renderers.
 * Foundation for the Soft Depth chart redesign.
 */
import * as d3 from 'd3';
import { prefersReducedMotion } from './motion.ts';

/**
 * Theme color palette resolved from CSS custom properties.
 */
export interface Palette {
  ok: string;
  okLight?: string;
  warn: string;
  warnLight?: string;
  err: string;
  errLight?: string;
  info: string;
  neutral: string;
  text: string;
  text2: string;
  text3: string;
}

/**
 * Resolves the current theme's color palette from CSS custom properties.
 */
export function resolvePalette(container: HTMLElement): Palette {
  const cs = getComputedStyle(container);
  return {
    ok: cs.getPropertyValue('--c-ok').trim(),
    okLight: cs.getPropertyValue('--c-ok-light').trim() || cs.getPropertyValue('--c-ok').trim(),
    warn: cs.getPropertyValue('--c-warn').trim(),
    warnLight: cs.getPropertyValue('--c-warn-light').trim() || cs.getPropertyValue('--c-warn').trim(),
    err: cs.getPropertyValue('--c-err').trim(),
    errLight: cs.getPropertyValue('--c-err-light').trim() || cs.getPropertyValue('--c-err').trim(),
    info: cs.getPropertyValue('--c-info').trim(),
    neutral: cs.getPropertyValue('--c-neutral').trim(),
    text: cs.getPropertyValue('--c-text').trim(),
    text2: cs.getPropertyValue('--c-text-2').trim(),
    text3: cs.getPropertyValue('--c-text-3').trim(),
  };
}

/**
 * Defines linear gradients (light→base) in an SVG's defs for semantic colors.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function defineGradients(svg: any, pal: Palette): void {
  const defs = svg.append('defs');
  const stops: Array<[string, string, string]> = [
    ['grad-ok', pal.okLight || pal.ok, pal.ok],
    ['grad-warn', pal.warnLight || pal.warn, pal.warn],
    ['grad-err', pal.errLight || pal.err, pal.err],
    ['grad-neutral', pal.neutral, pal.neutral],
    ['grad-info', pal.info, pal.info],
  ];
  for (const [id, from, to] of stops) {
    const g = defs.append('linearGradient').attr('id', id).attr('x1', '0').attr('y1', '0').attr('x2', '1').attr('y2', '1');
    g.append('stop').attr('offset', '0').attr('stop-color', from);
    g.append('stop').attr('offset', '1').attr('stop-color', to);
  }
}

/**
 * Checks if the user has requested reduced motion.
 *
 * Re-exported from lib/motion.ts so the chart renderers keep their existing import while
 * the navbar and the table of contents -- neither of which should pull d3 into the entry
 * chunk -- can ask the same question of the same one implementation.
 */
export { prefersReducedMotion };

/**
 * Animates a selection's attribute from→to with easing, or sets it immediately if reduced motion.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function animateIn(sel: any, o: { attr: string; from: number; to: (d: any, i: number) => number; dur?: number }): void {
  if (prefersReducedMotion()) {
    sel.attr(o.attr, o.to);
    return;
  }
  sel.attr(o.attr, o.from).transition().duration(o.dur ?? 500).ease(d3.easeCubicOut).attr(o.attr, o.to);
}

/**
 * Renders an empty state message box in the container.
 */
export function emptyState(container: HTMLElement, message: string): void {
  const box = document.createElement('div');
  box.className = 'state-box';
  box.textContent = message;
  container.appendChild(box);
}

/**
 * Shared tooltip helper for D3 charts.
 * Creates/updates a positioned tooltip div that matches the app's [data-tip] style.
 */
class ChartTooltip {
  private el: HTMLDivElement;
  private container: HTMLElement | null = null;

  constructor() {
    this.el = document.createElement('div');
    // max-width + wrap so a long label can't render as one line that runs off
    // the page (html/body are overflow-x:clip, so the spill would be cut).
    this.el.style.cssText = `
      position: absolute;
      padding: 6px 14px;
      max-width: min(320px, 90vw);
      background: var(--c-surface-raised);
      color: var(--c-text);
      font-size: var(--text-xs);
      font-weight: 500;
      white-space: normal;
      overflow-wrap: anywhere;
      border-radius: var(--radius-xs);
      border: 1px solid var(--c-border);
      box-shadow: var(--shadow-md);
      pointer-events: none;
      opacity: 0;
      transition: opacity 150ms ease;
      z-index: 1000;
    `;
  }

  show(content: string, x: number, y: number) {
    this.el.textContent = content;
    this.el.style.opacity = '1';
    // Clamp within the container (coords are container-relative) so the tooltip
    // never overflows the chart edge and gets clipped by the page.
    const cw = this.container?.clientWidth ?? Infinity;
    const left = Math.max(0, Math.min(x, cw - this.el.offsetWidth));
    this.el.style.left = `${left}px`;
    this.el.style.top = `${Math.max(0, y)}px`;
  }

  hide() {
    this.el.style.opacity = '0';
  }

  attach(container: HTMLElement) {
    container.style.position = 'relative';
    container.appendChild(this.el);
    this.container = container;
  }
}

/**
 * Creates and returns a new shared tooltip instance.
 */
export function sharedTooltip(): ChartTooltip {
  return new ChartTooltip();
}
