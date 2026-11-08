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

/** C8a: a chip elided to share a lane's room keeps at least 8rem; a lone first chip 90px. */
export const CHIP_FLOOR = 128;
export const FIRST_CHIP_FLOOR = 90;

/**
 * planChips decides how many of a lane's chips to name and how wide each may be (C8a):
 * up to `cap`, sharing `room` (gaps included) by water-filling, so a short name keeps
 * its width and the long ones split what is left. A chip cut below its natural width is
 * elided (C6) and must keep CHIP_FLOOR, or FIRST_CHIP_FLOOR when it is the only one; the
 * plan names fewer chips until that holds, and none when even one cannot.
 */
export function planChips(natural: number[], room: number, cap: number, gap = 8): { shown: number; widths: number[] } {
  for (let k = Math.min(cap, natural.length); k >= 1; k--) {
    let left = room - (k - 1) * gap;
    const widths = natural.slice(0, k);
    const order = widths.map((_, i) => i).sort((a, b) => widths[a] - widths[b]);
    order.forEach((i, j) => {
      const fair = Math.floor(left / (k - j));
      widths[i] = Math.min(widths[i], fair);
      left -= widths[i];
    });
    const floor = k > 1 ? CHIP_FLOOR : FIRST_CHIP_FLOOR;
    if (widths.every((w, i) => w >= natural[i] || w >= floor)) return { shown: k, widths };
  }
  return { shown: 0, widths: [] };
}

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

type Pt = [number, number];

/** The cubic's control points for a wire between two sides (a straight line when both are centres). */
function controls([x1, y1]: Pt, fs: Side, [x2, y2]: Pt, ts: Side): [Pt, Pt] | null {
  if (horiz(fs) && horiz(ts)) { const mx = (x1 + x2) / 2; return [[mx, y1], [mx, y2]]; }
  if (vert(fs) && vert(ts)) { const my = (y1 + y2) / 2; return [[x1, my], [x2, my]]; }
  if (vert(fs) && horiz(ts)) return [[x1, y2], [x1, y2]];
  if (horiz(fs) && vert(ts)) return [[x2, y1], [x2, y1]];
  return null;
}

/** wirePath is a smooth path between two anchors; an elbow when it turns a corner. */
export function wirePath(a: Pt, fs: Side, b: Pt, ts: Side): string {
  const c = controls(a, fs, b, ts);
  return c ? `M${a[0]} ${a[1]} C${c[0][0]} ${c[0][1]} ${c[1][0]} ${c[1][1]} ${b[0]} ${b[1]}` : `M${a[0]} ${a[1]} L${b[0]} ${b[1]}`;
}

const r2 = (v: number) => Math.round(v * 100) / 100;

/** wirePoint is the point at parameter t (0 at the source) of the wire wirePath draws. */
export function wirePoint(a: Pt, fs: Side, b: Pt, ts: Side, t: number): Pt {
  const c = controls(a, fs, b, ts);
  if (!c) return [r2(a[0] + (b[0] - a[0]) * t), r2(a[1] + (b[1] - a[1]) * t)];
  const u = 1 - t;
  const at = (i: 0 | 1) => u * u * u * a[i] + 3 * u * u * t * c[0][i] + 3 * u * t * t * c[1][i] + t * t * t * b[i];
  return [r2(at(0)), r2(at(1))];
}

/** C12: a missing wire's ✕ sits at 35% of the wire from its source, never near a convergence. */
export const BREAK_AT = 0.35;

/** breakMark is the ✕ of a broken wire, centred on p, half-size h. */
export const breakMark = ([x, y]: Pt, h = 4) => `M${r2(x - h)} ${r2(y - h)} L${r2(x + h)} ${r2(y + h)} M${r2(x + h)} ${r2(y - h)} L${r2(x - h)} ${r2(y + h)}`;

/**
 * arrowHead is the filled head of a wire that means "moves to" (C13b): a triangle with
 * its tip on the target anchor, pointing the way the wire enters it.
 */
export function arrowHead(a: Pt, fs: Side, b: Pt, ts: Side, len = 7, half = 4): string {
  const c = controls(a, fs, b, ts);
  const from = c ? (c[1][0] === b[0] && c[1][1] === b[1] ? c[0] : c[1]) : a;
  let dx = b[0] - from[0], dy = b[1] - from[1];
  const L = Math.hypot(dx, dy) || 1;
  dx /= L; dy /= L;
  const bx = b[0] - dx * len, by = b[1] - dy * len;
  return `M${r2(b[0])} ${r2(b[1])} L${r2(bx - dy * half)} ${r2(by + dx * half)} L${r2(bx + dy * half)} ${r2(by - dx * half)} Z`;
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
  /** The wire means "moves to", so it ends in an arrowhead (C13b). */
  head?: boolean;
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

/**
 * Kind glyphs (SPEC 3, Glyph): one outline icon per kind of thing, on a 24-unit box,
 * drawn in currentColor. They name a kind, never a state, so they carry no tone.
 */
export const KIND_GLYPH = {
  service: 'M4 7 L12 3 L20 7 L20 17 L12 21 L4 17 Z M4 7 L12 11 L20 7 M12 11 V21',
  revision: 'M4 9 L12 5 L20 9 L12 13 Z M4 13 L12 17 L20 13 M4 17 L12 21 L20 17',
  target: 'M4 6 H20 V15 H4 Z M9 19 H15 M12 15 V19',
  owner: 'M12 11 A3.5 3.5 0 1 0 12 4 A3.5 3.5 0 1 0 12 11 Z M5 20 C5 15.5 19 15.5 19 20',
  domain: 'M4 4 H11 V11 H4 Z M13 4 H20 V11 H13 Z M4 13 H11 V20 H4 Z M13 13 H20 V20 H13 Z',
  offering: 'M3 9 L12 4 L21 9 L12 14 Z M6 11 V17 L12 20 L18 17 V11',
  'golden-path': 'M4 20 C8 20 8 12 12 12 C16 12 16 4 20 4 M17 4 H20 V7',
  source: 'M12 20 V12 M12 12 A3 3 0 1 0 12 6 A3 3 0 1 0 12 12 Z M6 4 A9 9 0 0 0 6 14 M18 4 A9 9 0 0 1 18 14',
  environment: 'M3 18 H21 M5 18 V10 L12 6 L19 10 V18 M10 18 V13 H14 V18',
} as const;
export type KindGlyph = keyof typeof KIND_GLYPH;

/** sharePct prints a share of a total: "<1%" for a non-zero share under 1%, never "0%". */
export function sharePct(n: number, total: number): string {
  if (!total || !n) return '0%';
  const p = (n / total) * 100;
  return p < 1 ? '<1%' : `${Math.round(p)}%`;
}
