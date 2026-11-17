/**
 * The one chart tooltip (#597, Decision 9). A mark opts in with `use:chartTip={model}`;
 * the page mounts one ChartTip that renders whatever this store holds. Hover, focus and
 * a tap show the same words, Escape and blur hide them, and every number it shows is
 * also in the card's table view, so nothing depends on hovering.
 */
import { writable } from 'svelte/store';
import type { Action } from 'svelte/action';

/** One readout row: the value leads, the label follows; the key is a short line stroke. */
export interface TipRow {
  value: string;
  label: string;
  /** A CSS colour for the line key, e.g. `var(--chart-1)`. */
  color?: string;
  /** A texture key instead of a line: the hatch (unknown) or the dots (unverified). */
  texture?: 'hatch' | 'dots';
}

export interface TipModel {
  title: string;
  rows: TipRow[];
  foot?: string;
}

export interface TipOptions {
  /** `img` (default) names an SVG hit target; `false` leaves the element's own role
   *  (a table cell) and points `aria-describedby` at the tooltip instead. */
  role?: 'img' | false;
  /** Adds `tabindex="0"`; defaults to true unless `role` is false (that caller owns its
   *  tab stops, as RateGrid's roving tabindex does). */
  focusable?: boolean;
}

export type TipParam = TipModel & TipOptions;

export interface TipState {
  model: TipModel;
  x: number;
  y: number;
  owner: Element;
}

export const TIP_ID = 'chart-tip';
export const tipState = writable<TipState | null>(null);

let current: Element | null = null;
tipState.subscribe((s) => { current = s?.owner ?? null; });

export const hideTip = () => tipState.set(null);

/** tipLabel is the accessible name of a mark: title, each row, then the foot. */
export function tipLabel(m: TipModel): string {
  return [m.title, ...m.rows.map((r) => `${r.label} ${r.value}`), m.foot].filter(Boolean).join(', ');
}

export const chartTip: Action<Element, TipParam> = (node, param) => {
  let p = param;
  const show = (x: number, y: number) => tipState.set({ model: p, x, y, owner: node });
  const atNode = () => { const r = node.getBoundingClientRect(); show(r.left + r.width / 2, r.top); };
  const touch = (e: Event) => (e as PointerEvent).pointerType === 'touch';

  const apply = () => {
    if (p.role === false) {
      node.setAttribute('aria-describedby', TIP_ID);
    } else {
      node.setAttribute('role', p.role ?? 'img');
      node.setAttribute('aria-label', tipLabel(p));
    }
    if (p.focusable ?? p.role !== false) node.setAttribute('tabindex', '0');
  };

  const on: Record<string, (e: Event) => void> = {
    pointerenter: (e) => { if (!touch(e)) show((e as MouseEvent).clientX, (e as MouseEvent).clientY); },
    pointermove: (e) => { if (!touch(e) && current === node) show((e as MouseEvent).clientX, (e as MouseEvent).clientY); },
    pointerleave: (e) => { if (!touch(e) && current === node) hideTip(); },
    // A tap toggles; a tap elsewhere hides it (ChartTip listens for that one).
    pointerup: (e) => { if (touch(e)) { if (current === node) hideTip(); else show((e as MouseEvent).clientX, (e as MouseEvent).clientY); } },
    focus: atNode,
    blur: () => { if (current === node) hideTip(); },
    keydown: (e) => { if ((e as KeyboardEvent).key === 'Escape' && current === node) hideTip(); },
  };

  apply();
  for (const [k, f] of Object.entries(on)) node.addEventListener(k, f);
  return {
    update(next) {
      p = next;
      apply();
      if (current === node) tipState.update((s) => s && { ...s, model: p });
    },
    destroy() {
      for (const [k, f] of Object.entries(on)) node.removeEventListener(k, f);
      if (current === node) hideTip();
    },
  };
};
