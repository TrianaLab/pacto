/**
 * Figure geometry (SPEC 3): pure functions that return marks and paths. Nothing here
 * measures text, because no figure puts words in its SVG: the drawing draws marks and
 * HTML carries the words (C1). Wires are drawn at 1 SVG unit = 1 CSS px between anchor
 * points read from the HTML grid, so text never scales with a drawing (C5).
 */

/** slab is a 2:1 diamond of half-width hw and thickness t, as the landing hero draws it. */
export function slab(cx: number, cy: number, hw: number, t: number) {
  const hh = hw / 2;
  const top = `M${cx} ${cy - hh} L${cx + hw} ${cy} L${cx} ${cy + hh} L${cx - hw} ${cy} Z`;
  return {
    top,
    left: `M${cx - hw} ${cy} L${cx} ${cy + hh} L${cx} ${cy + hh + t} L${cx - hw} ${cy + t} Z`,
    right: `M${cx} ${cy + hh} L${cx + hw} ${cy} L${cx + hw} ${cy + t} L${cx} ${cy + hh + t} Z`,
    edge: `${top} M${cx - hw} ${cy} V${cy + t} L${cx} ${cy + hh + t} L${cx + hw} ${cy + t} V${cy} M${cx} ${cy + hh} V${cy + hh + t}`,
  };
}

/** ringPath is the alarm ring around a slab: its top face, 12 units wider. */
export const ringPath = (cx: number, cy: number, hw: number) => slab(cx, cy, hw + 12, 0).top;

/**
 * stackGeometry lays out 1 to 5 thin layers, top first, in a fixed-aspect cell so the
 * 2:1 geometry never distorts. Each layer's anchor is its right corner, where a wire
 * leaves it. The height includes the bottom layer's side, so nothing spills its cell.
 */
export const MAX_STACK_LAYERS = 5;
export function stackGeometry(layers: number, w = 150) {
  const n = Math.max(1, Math.min(MAX_STACK_LAYERS, layers));
  const hw = 52, t = 9, gap = 24, cx = w / 2, top = 36;
  const H = top + (n - 1) * gap + hw / 2 + t + 12;
  return {
    W: w, H,
    layers: Array.from({ length: n }, (_, k) => {
      const y = top + k * gap;
      return { y, paths: slab(cx, y, hw, t), anchor: { x: cx + hw, y: y + t / 2 } };
    }),
  };
}

/** S1: a figure degrades by its own width. */
export type WidthClass = 'wide' | 'mid' | 'narrow' | 'col';
export const widthClass = (w: number): WidthClass => (w >= 800 ? 'wide' : w >= 560 ? 'mid' : w >= 480 ? 'narrow' : 'col');
/** S1: named chips per lane at each width. */
export const CHIP_CAP: Record<WidthClass, number> = { wide: 4, mid: 2, narrow: 0, col: 0 };

/** Where a wire leaves or enters a box: its left, right, top or bottom middle, or centre. */
export type Side = 'l' | 'r' | 't' | 'b' | 'c';
type Box = { left: number; top: number; right: number; bottom: number; width: number; height: number };

export function anchorPoint(r: Box, side: Side, origin: { left: number; top: number }): [number, number] {
  const cx = r.left + r.width / 2 - origin.left;
  const cy = r.top + r.height / 2 - origin.top;
  if (side === 'r') return [r.right - origin.left, cy];
  if (side === 'l') return [r.left - origin.left, cy];
  if (side === 't') return [cx, r.top - origin.top];
  if (side === 'b') return [cx, r.bottom - origin.top];
  return [cx, cy];
}

const horiz = (s: Side) => s === 'l' || s === 'r';
const vert = (s: Side) => s === 't' || s === 'b';

/** wirePath is a smooth path between two anchors; an elbow when it turns a corner. */
export function wirePath([x1, y1]: [number, number], fs: Side, [x2, y2]: [number, number], ts: Side): string {
  if (horiz(fs) && horiz(ts)) {
    const mx = (x1 + x2) / 2;
    return `M${x1} ${y1} C${mx} ${y1} ${mx} ${y2} ${x2} ${y2}`;
  }
  if (vert(fs) && vert(ts)) {
    const my = (y1 + y2) / 2;
    return `M${x1} ${y1} C${x1} ${my} ${x2} ${my} ${x2} ${y2}`;
  }
  if (vert(fs) && horiz(ts)) return `M${x1} ${y1} C${x1} ${y2} ${x1} ${y2} ${x2} ${y2}`;
  if (horiz(fs) && vert(ts)) return `M${x1} ${y1} C${x2} ${y1} ${x2} ${y1} ${x2} ${y2}`;
  return `M${x1} ${y1} L${x2} ${y2}`;
}

/** SPEC 2.1: observed solid, projected dashed, stale dotted, missing broken. */
export type WireStyle = 'obs' | 'proj' | 'stale' | 'broken';
export type FigTone = 'err' | 'warn' | 'ok' | 'accent' | 'neutral';

export interface WireDef {
  id: string;
  from: string;
  to: string;
  fs?: Side;
  ts?: Side;
  style: WireStyle;
  tone: FigTone;
  /** Overrides in the vertical (col) layout. */
  col?: { from?: string; to?: string; fs?: Side; ts?: Side };
  /** Width classes the wire is drawn in; all when absent. */
  when?: WidthClass[];
}

/**
 * State glyphs as geometry centred on 0,0 in a 16-unit box (C15), so they sit dead
 * centre in any font. A Node draws one of these; no figure prints a glyph character.
 */
export const GLYPH_PATH = {
  err: 'M-3.2 -3.2 L3.2 3.2 M3.2 -3.2 L-3.2 3.2',
  ok: 'M-3.7 0.2 L-1.1 2.9 L3.8 -2.9',
  warn: 'M0 -4.2 V1.3 M0 3.9 V4',
  unknown: 'M-2.5 -1.7 C-2.5 -4.6 2.6 -4.6 2.6 -1.7 C2.6 0 0 0.3 0 2.1 M0 4.3 V4.4',
  held: 'M-1.9 -3.4 V3.4 M1.9 -3.4 V3.4',
  none: 'M-3.4 0 H3.4',
} as const;
export type GlyphName = keyof typeof GLYPH_PATH;

/** sharePct prints a share of a total: "<1%" for a non-zero share under 1%, never "0%". */
export function sharePct(n: number, total: number): string {
  if (!total || !n) return '0%';
  const p = (n / total) * 100;
  return p < 1 ? '<1%' : `${Math.round(p)}%`;
}
