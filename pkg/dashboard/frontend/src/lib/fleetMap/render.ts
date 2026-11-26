/**
 * Draws the fleet map on a 2D canvas, ported from the approved mockup
 * (docs/superpowers/mockups/fleet-graph-semantic-zoom.html, rendering).
 *
 * Groups level: one neutral bubble per group with its match ring, attention
 * pills and a centred label, and one thin, low-contrast ribbon per group pair
 * split by how declared and observed agree. Services level: the bubbles fade
 * to containers and each service is a dot, neutral unless it needs attention.
 * Names level: each service that has room is named with its version, worst
 * first; a name that does not fit is omitted, never squeezed. Details level:
 * a card per service with its owner, interfaces, what Pacto knows of it and
 * its worst attention item. The levels crossfade over their thresholds.
 * Inside a group, dependencies are drawn from the Names level on, or for the
 * service under the pointer, each in the line style of its match state.
 * Colour never stands alone: a ring is solid for its matched share and dashed
 * for the rest, a pill carries a glyph and a count, an error dot a double
 * ring, a warning a single ring, every match state a line style (LEGEND).
 *
 * Every drawn label is recorded with its rectangle and font size, so a
 * browser test can check that none overlap, cross the edge or shrink below
 * the smallest type step.
 */
import { countOf, formatCount } from '../format';
import { attentionCategoryLabel } from '../entityLabels';
import { edgeVisible, groupCount, isSpecial, ribbonCount, type Lens, type MapEdge, type MapGroup, type MapNode, type MapRibbon, type Visibility, nodeVisibility } from './lens';
import { edgeEmphasis, groupEmphasis, groupModeLine, nodeEmphasis, ribbonEmphasis, type GapKind, type Mode } from './modes';
import { L1, L2, L3, NODE_R, gapOf, smooth, toScreen, toWorld, type View } from './view';
import type { GroupPage } from './store.svelte';

export interface Palette {
  bg: string; raised: string; border: string; borderStrong: string;
  text: string; text2: string; text3: string;
  ok: string; warn: string; err: string; info: string; neutral: string; accent: string; errBg: string;
  sans: string; display: string; dark: boolean;
}

/** readPalette reads the design tokens the canvas draws with. */
export function readPalette(el: Element = document.documentElement): Palette {
  const cs = getComputedStyle(el);
  const g = (n: string) => cs.getPropertyValue(n).trim();
  return {
    bg: g('--c-surface'), raised: g('--c-surface-raised'), border: g('--c-border'), borderStrong: g('--c-text-3'),
    text: g('--c-text'), text2: g('--c-text-2'), text3: g('--c-text-3'),
    ok: g('--c-ok'), warn: g('--c-warn'), err: g('--c-err'), info: g('--c-info'), neutral: g('--c-neutral'),
    accent: g('--c-accent'), errBg: g('--c-err-border'),
    sans: g('--font-sans') || 'sans-serif', display: g('--font-display') || 'serif',
    dark: document.documentElement.dataset.theme !== 'light',
  };
}

/** A node placed in the world: its group's centre plus its offset, and what the lens makes of it. */
export interface PlacedNode {
  node: MapNode;
  group: MapGroup;
  x: number;
  y: number;
  vis: Visibility;
}

/** placeNodes puts a group page's nodes in the world. The lens decides visibility only. */
export function placeNodes(group: MapGroup, page: GroupPage, lens: Lens): PlacedNode[] {
  return page.nodes.map((node) => ({ node, group, x: group.x + node.x, y: group.y + node.y, vis: nodeVisibility(node, lens) }));
}

/** A dependency inside a group, between two placed nodes. */
export interface PlacedEdge { edge: MapEdge; a: PlacedNode; b: PlacedNode }

/** placeEdges joins a group page's edges to its placed nodes (an edge names nodes by position in the group). */
export function placeEdges(page: GroupPage, nodes: readonly PlacedNode[]): PlacedEdge[] {
  const out: PlacedEdge[] = [];
  for (const edge of page.edges) {
    const a = nodes[edge.from], b = nodes[edge.to];
    if (a && b) out.push({ edge, a, b });
  }
  return out;
}

export type Rect = [number, number, number, number];

/** One drawn label: its text, rectangle in CSS pixels and font size in pixels. */
export interface DrawnLabel { text: string; x: number; y: number; w: number; h: number; size: number; font: string }

export interface Scene {
  groups: readonly MapGroup[];
  ribbons: readonly MapRibbon[];
  nodes: readonly PlacedNode[];
  view: View;
  w: number;
  h: number;
  dpr: number;
  palette: Palette;
  lens: Lens;
  groupBy: string;
  /** Dependencies inside the groups in view. */
  edges?: readonly PlacedEdge[];
  /** The emphasis mode: what it does not emphasise stays as grey context. */
  mode?: Mode;
  /** The environment picked: services not running there are grey context. */
  environment?: string;
  hoverGroup?: string | null;
  /** The service under the pointer: its dependencies show at every level. */
  hoverNode?: string | null;
  /** Screen areas the HUD covers: no label is drawn there. */
  reserved?: readonly Rect[];
}

export interface RenderResult { labels: DrawnLabel[]; nodes: number; ribbons: number; edges: number }

/** The smallest text the map draws, in px: above the 12.19px floor of the type ramp. */
export const MIN_FONT = 13;


/**
 * Line dash per match state: matched solid, expected-only dashed, observed-only
 * dotted, insufficient fine dots; a call across environments dash-dotted.
 */
export const EDGE_DASH = { matched: [] as number[], expectedOnly: [5, 4], observedOnly: [1.5, 3], insufficient: [1, 2], crossEnvironment: [8, 3, 2, 3] };
/** Ring dash per kind of service drawn hollow (an external system's ring is solid). */
export const NODE_DASH = { inferred: [2, 2], expectedOnly: [4, 2], insufficient: [1, 3] };

/** Glyphs that carry severity beside colour. */
export const SEVERITY_GLYPH = { error: '✕', warning: '▲', info: 'i' } as const;

/** One legend entry and the cue that carries it beside colour: a shape, a glyph or a dash. */
export interface LegendEntry { key: string; label: string; shape: 'dot' | 'ring' | 'ring2' | 'line' | 'badge'; glyph?: string; dash?: readonly number[]; tone: keyof Palette }

/** The map's legend, every state it draws. No two entries differ by colour alone. */
export const LEGEND: readonly LegendEntry[] = [
  { key: 'ok', label: 'Nothing to do', shape: 'dot', tone: 'text3' },
  { key: 'error', label: 'Error', shape: 'ring2', glyph: SEVERITY_GLYPH.error, tone: 'err' },
  { key: 'warning', label: 'Warning', shape: 'ring', glyph: SEVERITY_GLYPH.warning, tone: 'warn' },
  { key: 'expected-only', label: 'Declared, nothing running', shape: 'ring', dash: NODE_DASH.expectedOnly, tone: 'info' },
  { key: 'inferred', label: 'Inferred (runs without a contract)', shape: 'ring', dash: NODE_DASH.inferred, tone: 'neutral' },
  { key: 'insufficient', label: 'Insufficient evidence', shape: 'ring', dash: NODE_DASH.insufficient, tone: 'neutral' },
  { key: 'external', label: 'External system', shape: 'ring', tone: 'text2' },
  { key: 'outside-contract', label: 'Resources outside contract', shape: 'badge', glyph: '#', tone: 'text2' },
  { key: 'matched', label: 'Matched', shape: 'line', dash: EDGE_DASH.matched, tone: 'text3' },
  { key: 'expected-not-observed', label: 'Expected, not observed', shape: 'line', dash: EDGE_DASH.expectedOnly, tone: 'info' },
  { key: 'observed-not-expected', label: 'Observed, not expected', shape: 'line', dash: EDGE_DASH.observedOnly, tone: 'warn' },
  { key: 'insufficient-edge', label: 'No evidence either way', shape: 'line', dash: EDGE_DASH.insufficient, tone: 'neutral' },
  { key: 'problem', label: 'Problem on the dependency', shape: 'line', glyph: '!', tone: 'err' },
  { key: 'cross-environment', label: 'Call across environments', shape: 'line', dash: EDGE_DASH.crossEnvironment, tone: 'err' },
];

/** Occupancy of screen rectangles, so labels never overlap and never cross the canvas edge. */
class Occ {
  private m = new Map<number, Rect[]>();
  constructor(private w: number, private h: number) {}
  private cells(r: Rect, f: (k: number) => void) {
    for (let x = Math.floor(r[0] / 64); x <= Math.floor(r[2] / 64); x++) for (let y = Math.floor(r[1] / 64); y <= Math.floor(r[3] / 64); y++) f(x * 100000 + y);
  }
  /** count is how many held rectangles r overlaps. */
  count(r: Rect): number {
    const seen = new Set<Rect>();
    this.cells(r, (k) => { for (const q of this.m.get(k) ?? []) if (r[0] < q[2] && r[2] > q[0] && r[1] < q[3] && r[3] > q[1]) seen.add(q); });
    return seen.size;
  }
  free(r: Rect): boolean { return this.count(r) === 0; }
  add(r: Rect) { this.cells(r, (k) => { const a = this.m.get(k); if (a) a.push(r); else this.m.set(k, [r]); }); }
  tryAdd(r: Rect): boolean {
    if (r[0] < 0 || r[1] < 0 || r[2] > this.w || r[3] > this.h || !this.free(r)) return false;
    this.add(r);
    return true;
  }
}

const widths = new Map<string, number>();
/** clearWidths forgets every measured width: a webfont that arrived late draws wider or narrower than its fallback. */
export function clearWidths(): void { widths.clear(); }
function textW(ctx: CanvasRenderingContext2D, s: string, font: string): number {
  const k = `${font}|${s}`;
  let w = widths.get(k);
  if (w === undefined) { ctx.font = font; w = ctx.measureText(s).width; widths.set(k, w); }
  return w;
}

const wOf = (n: number) => (n ? Math.min(5, 0.6 + Math.sqrt(n) * 0.35) : 0);

/** isDim says whether a service is grey context: the mode does not emphasise it, or it does not run in the environment picked. */
const isDim = (s: Scene, p: PlacedNode): boolean =>
  !nodeEmphasis(p.node, s.mode ?? null) || (!!s.environment && p.node.inEnvironment === false);

/** render draws one frame. */
export function render(ctx: CanvasRenderingContext2D, s: Scene): RenderResult {
  const P = s.palette, { k } = s.view, g = gapOf(k);
  const labels: DrawnLabel[] = [];
  ctx.setTransform(s.dpr, 0, 0, s.dpr, 0, 0);
  ctx.globalAlpha = 1;
  ctx.fillStyle = P.bg;
  ctx.fillRect(0, 0, s.w, s.h);
  const a1 = smooth(g, L1 * 0.88, L1 * 1.12), a2 = smooth(g, L2 * 0.85, L2 * 1.15), a3 = smooth(g, L3 * 0.85, L3 * 1.1);
  const [wx0, wy0] = toWorld(s.view, -40, -40), [wx1, wy1] = toWorld(s.view, s.w + 40, s.h + 40);
  const inView = (x: number, y: number, m = 0) => x > wx0 - m && x < wx1 + m && y > wy0 - m && y < wy1 + m;
  const byKey = new Map(s.groups.map((gr) => [gr.key, gr]));
  const hg = s.hoverGroup && a1 < 0.5 ? s.hoverGroup : null;

  const ribbons = drawRibbons(ctx, s, byKey, a1, hg);
  for (const gr of s.groups) if (inView(gr.x, gr.y, gr.r)) drawBubble(ctx, s, gr, a1, hg);
  const edges = a1 > 0 ? drawEdges(ctx, s, a1, a2, inView) : 0;
  const nodes = a1 > 0 ? drawDots(ctx, s, a1, inView) : 0;

  const occ = new Occ(s.w, s.h);
  for (const r of s.reserved ?? []) occ.add(r);
  const halo: Halo = (txt, x, y, size, font, color, alpha, align) => {
    ctx.font = font; ctx.globalAlpha = alpha; ctx.textAlign = align; ctx.textBaseline = 'middle';
    ctx.lineJoin = 'round'; ctx.lineWidth = 3; ctx.strokeStyle = P.bg; ctx.strokeText(txt, x, y);
    ctx.fillStyle = color; ctx.fillText(txt, x, y);
    const w = textW(ctx, txt, font);
    const left = align === 'center' ? x - w / 2 : x;
    labels.push({ text: txt, x: left, y: y - size * 0.575, w, h: size * 1.15, size, font });
  };
  const shown = a1 > 0.5 ? s.nodes.filter((p) => p.vis && inView(p.x, p.y)) : [];
  const rpx = dotRadius(k);
  // Cards first: each needs room around its own service only.
  const carded = new Set<PlacedNode>();
  if (a3 > 0 && shown.length) {
    const dots = new Occ(Infinity, Infinity);
    for (const p of shown) { const [sx, sy] = toScreen(s.view, p.x, p.y); dots.add([sx - rpx, sy - rpx, sx + rpx, sy + rpx]); }
    for (const p of shown.filter((q) => q.vis === 1).sort(prio)) {
      if (carded.size >= MAX_CARDS) break;
      if (drawCard(ctx, s, p, occ, dots, a3, halo)) carded.add(p);
    }
  }
  // Dots claim their space next, so a label never sits on a service.
  for (const p of shown) {
    const [sx, sy] = toScreen(s.view, p.x, p.y), r0 = rpx + (isProblem(p.node) ? 3 : 1);
    occ.add([sx - r0, sy - r0, sx + r0, sy + r0]);
  }
  // An external system with managed resources no offering produced (517-S7) gets a
  // small count badge at its corner, before names take the room around it.
  if (a2 > 0) for (const p of shown) if (p.vis === 1 && p.node.external && (p.node.outsideContract ?? 0) > 0) outsideBadge(ctx, s, p, rpx, a2, occ, halo);
  if (a2 > 0) {
    let n = 0;
    for (const p of shown.filter((q) => q.vis === 1 && !carded.has(q)).sort(prio)) {
      if (n >= MAX_NAMES) break;
      if (labelNode(ctx, s, p, a2, rpx, occ, halo)) n++;
    }
  }
  const order = s.groups.filter((gr) => inView(gr.x, gr.y, gr.r)).sort((a, b) => b.services - a.services);
  for (const gr of order) {
    if (a1 < 1) groupLabel(ctx, s, gr, 1 - a1, occ, halo, s.groups.length > 40);
    if (a1 > 0) groupCaption(ctx, s, gr, a1, occ, halo);
    if (a1 > 0.5) subLabels(ctx, s, gr, a1, occ, halo);
  }
  ctx.globalAlpha = 1;
  ctx.setLineDash([]);
  return { labels, nodes, ribbons, edges };
}

type Halo = (txt: string, x: number, y: number, size: number, font: string, color: string, alpha: number, align: CanvasTextAlign) => void;

/** At most this many cards and names a frame: the rest are omitted. */
const MAX_CARDS = 220;
const MAX_NAMES = 160;

const isProblem = (n: MapNode) => n.severity === 'error' || n.severity === 'warning';
const sevRank = (n: MapNode) => (n.severity === 'error' ? 2 : n.severity === 'warning' ? 1 : 0);

/** prio orders services for a name or a card: worst first, declared before inferred, busiest, then by key. */
function prio(a: PlacedNode, b: PlacedNode): number {
  const x = a.node, y = b.node;
  return sevRank(y) - sevRank(x) || Number(!!x.inferred) - Number(!!y.inferred)
    || ((y.in ?? 0) + (y.out ?? 0)) - ((x.in ?? 0) + (x.out ?? 0)) || (x.key < y.key ? -1 : x.key > y.key ? 1 : 0);
}

/** The ribbon strands of Both: field, gap kind, colour and dash. */
const STRANDS = [
  ['matched', null, 'text3', EDGE_DASH.matched],
  ['expectedOnly', 'expected-only', 'info', EDGE_DASH.expectedOnly],
  ['observedOnly', 'observed-only', 'warn', EDGE_DASH.observedOnly],
  ['insufficient', 'insufficient', 'neutral', EDGE_DASH.insufficient],
] as const satisfies readonly (readonly [keyof MapRibbon, GapKind | null, keyof Palette, number[]])[];

function drawRibbons(ctx: CanvasRenderingContext2D, s: Scene, byKey: Map<string, MapGroup>, a1: number, hg: string | null): number {
  const P = s.palette, k = s.view.k, m = s.mode ?? null;
  // Ribbons are the Groups level's edges; at the Services level they stay as faint bundles.
  const al = a1 < 1 ? 1 - a1 * 0.4 : 0.6;
  let drawn = 0;
  const curve = (A: MapGroup, B: MapGroup, off: number, w: number, color: string, alpha: number, dash: readonly number[]) => {
    const [ax, ay] = toScreen(s.view, A.x, A.y), [bx, by] = toScreen(s.view, B.x, B.y);
    const dx = bx - ax, dy = by - ay, len = Math.hypot(dx, dy) || 1, ux = dx / len, uy = dy / len, nx = -uy, ny = ux;
    const p0x = ax + ux * (A.r * k + 6) + nx * off, p0y = ay + uy * (A.r * k + 6) + ny * off;
    const p1x = bx - ux * (B.r * k + 6) + nx * off, p1y = by - uy * (B.r * k + 6) + ny * off;
    const bend = Math.min(40, len * 0.08);
    ctx.beginPath();
    ctx.moveTo(p0x, p0y);
    ctx.quadraticCurveTo((p0x + p1x) / 2 + nx * bend, (p0y + p1y) / 2 + ny * bend, p1x, p1y);
    // ponytail: the canvas dashes the whole path, off-screen part included; very long dashed curves draw solid.
    ctx.setLineDash(dash.length && len > Math.max(s.w, s.h) * 1.5 ? [] : [...dash]);
    ctx.lineWidth = w; ctx.strokeStyle = color; ctx.globalAlpha = al * alpha;
    ctx.stroke();
  };
  const baseA = P.dark ? 0.22 : 0.3;
  const gaps = m?.kind === 'gaps' ? m.gaps : null;
  for (const r of s.ribbons) {
    const A = byKey.get(r.from), B = byKey.get(r.to);
    const n = ribbonCount(r, s.lens);
    if (!A || !B || !n) continue;
    // Every environment calls the same external systems: those ribbons wait for a hover.
    if (s.groupBy === 'environment' && B.kind === 'external' && hg !== A.key) continue;
    drawn++;
    const hot = hg !== null && (r.from === hg || r.to === hg);
    const dim = hg !== null && !hot;
    if (hot) { curve(A, B, 0, wOf(n), r.from === hg ? P.accent : P.warn, 0.9, []); continue; }
    if (r.crossEnvironment) { curve(A, B, 0, Math.max(1.5, wOf(r.crossEnvironment)), P.err, 0.9, EDGE_DASH.crossEnvironment); continue; }
    if (m && !gaps) {
      // Problems or missing: grey context, with the problem share drawn strong.
      curve(A, B, 0, wOf(n), P.text3, 0.12, []);
      const e = ribbonEmphasis(r, m);
      if (e) curve(A, B, 0, wOf(e), P.err, 0.85, []);
      continue;
    }
    if (s.lens !== 'both') { curve(A, B, 0, wOf(n), P.text3, dim ? 0.08 : baseA, []); continue; }
    // Both: side-by-side strands by how declared and observed agree; gaps mode dims what it does not ask for.
    const strands = STRANDS.filter(([f]) => r[f]);
    const ws = strands.map(([f, gk]) => Math.max(1, wOf(Number(r[f])) * (gk ? 0.8 : 1)));
    let off = -ws.reduce((a, b) => a + b + 1.5, 0) / 2;
    strands.forEach(([, gk, tone, dash], i) => {
      off += ws[i] / 2;
      const off2 = gaps && (!gk || !gaps.has(gk));
      curve(A, B, off, ws[i], P[tone] as string, off2 || dim ? 0.08 : !gk ? baseA : gaps ? 0.85 : 0.5, dash);
      off += ws[i] / 2 + 1.5;
    });
  }
  ctx.setLineDash([]);
  ctx.globalAlpha = 1;
  return drawn;
}

function drawBubble(ctx: CanvasRenderingContext2D, s: Scene, gr: MapGroup, a1: number, hg: string | null) {
  const P = s.palette;
  const [sx, sy] = toScreen(s.view, gr.x, gr.y);
  const R = gr.r * s.view.k;
  const special = isSpecial(gr);
  const fade = a1 < 0.5 && !groupEmphasis(gr, s.mode ?? null) ? 0.5 : 1;
  ctx.beginPath();
  ctx.arc(sx, sy, R, 0, Math.PI * 2);
  ctx.globalAlpha = 1 - 0.6 * a1;
  ctx.fillStyle = special ? P.bg : P.raised;
  ctx.fill();
  ctx.globalAlpha = fade;
  ctx.lineWidth = hg === gr.key ? 2 : 1;
  ctx.strokeStyle = hg === gr.key ? P.accent : P.borderStrong;
  ctx.setLineDash(special ? [4, 4] : []);
  ctx.stroke();
  ctx.setLineDash([]);
  if (a1 >= 1 || special || R <= 10) return;
  // The match ring: dashed all round, solid over the matched share (not colour alone).
  const rr = R + 3.5;
  ctx.globalAlpha = (1 - a1) * fade;
  ctx.lineCap = 'butt';
  ctx.beginPath();
  ctx.arc(sx, sy, rr, 0, Math.PI * 2);
  ctx.strokeStyle = P.text3; ctx.setLineDash([2, 3]); ctx.lineWidth = 1.5;
  ctx.stroke();
  ctx.setLineDash([]);
  const pct = gr.match.percent;
  if (pct != null && pct > 0) {
    ctx.beginPath();
    ctx.arc(sx, sy, rr, -Math.PI / 2, -Math.PI / 2 + (Math.PI * 2 * pct) / 100);
    ctx.strokeStyle = P.text2; ctx.lineWidth = 3;
    ctx.stroke();
  }
  ctx.globalAlpha = 1;
}

/** Which stroke an edge inside a group gets. */
type EdgeBucket = 'ctx' | 'hot' | 'probE' | 'probW' | 'matched' | 'inferred' | 'expectedOnly' | 'observedOnly' | 'insufficient';
const DIFF_BUCKET: Record<string, EdgeBucket> = { matched: 'matched', 'expected-not-observed': 'expectedOnly', 'observed-not-expected': 'observedOnly', insufficient: 'insufficient' };

/**
 * drawEdges draws the dependencies inside the groups in view: from the Names
 * level on, or those of the service under the pointer. Matched solid,
 * expected-only dashed, observed-only dotted, insufficient fine dots, an
 * inferred one muted; a problem solid and loud, with a "!" mark at its middle.
 */
function drawEdges(ctx: CanvasRenderingContext2D, s: Scene, a1: number, a2: number, inView: (x: number, y: number) => boolean): number {
  const P = s.palette, m = s.mode ?? null, hot = s.hoverNode ?? null;
  const B = new Map<EdgeBucket, PlacedEdge[]>();
  let n = 0;
  for (const pe of s.edges ?? []) {
    const { a, b, edge: e } = pe;
    if (!a.vis || !b.vis || !edgeVisible(e, s.lens) || (!inView(a.x, a.y) && !inView(b.x, b.y))) continue;
    const mine = hot !== null && (a.node.key === hot || b.node.key === hot);
    if (!mine && a2 < 0.5) continue;
    const bk: EdgeBucket = mine ? 'hot'
      : !edgeEmphasis(e, m) || isDim(s, a) || isDim(s, b) ? 'ctx'
        : e.problem ? (e.problem.severity === 'error' ? 'probE' : 'probW')
          : s.lens !== 'both' ? 'matched'
            : e.inferred ? 'inferred'
              : DIFF_BUCKET[e.difference ?? 'matched'] ?? 'matched';
    const list = B.get(bk);
    if (list) list.push(pe); else B.set(bk, [pe]);
    n++;
  }
  const stroke = (bk: EdgeBucket, color: string, alpha: number, w: number, dash: readonly number[]) => {
    const list = B.get(bk);
    if (!list) return;
    ctx.globalAlpha = alpha; ctx.strokeStyle = color; ctx.lineWidth = w; ctx.setLineDash([...dash]);
    ctx.beginPath();
    for (const { a, b } of list) { const [ax, ay] = toScreen(s.view, a.x, a.y), [bx, by] = toScreen(s.view, b.x, b.y); ctx.moveTo(ax, ay); ctx.lineTo(bx, by); }
    ctx.stroke();
  };
  const quiet = hot ? 0.3 : 1;
  const base = a1 * quiet * (P.dark ? 0.12 : 0.15) * (1 + 0.5 * a2);
  const loud = m?.kind === 'gaps' ? a1 * 0.9 : a1 * quiet * (0.35 + 0.45 * a2);
  stroke('ctx', P.text3, a1 * 0.06, 1, []);
  stroke('inferred', P.neutral, base * 0.7, 1, EDGE_DASH.observedOnly);
  stroke('matched', P.text3, base, 1, EDGE_DASH.matched);
  stroke('insufficient', P.neutral, m?.kind === 'gaps' ? loud : base * 0.7, 1, EDGE_DASH.insufficient);
  stroke('expectedOnly', P.info, loud * 0.8, 1.1, EDGE_DASH.expectedOnly);
  stroke('observedOnly', P.warn, loud, 1.4, EDGE_DASH.observedOnly);
  stroke('probW', P.warn, loud, 1.6, []);
  stroke('probE', P.err, loud, 1.6, []);
  stroke('hot', P.accent, a1, 1.75, []);
  ctx.setLineDash([]);
  // Problem marks at the Names level: a "!" at the middle of each problem edge on screen.
  if (a2 > 0.3) {
    ctx.font = `700 ${MIN_FONT}px ${P.sans}`; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    let marks = 0;
    for (const bk of ['probE', 'probW'] as const) {
      const col = bk === 'probE' ? P.err : P.warn;
      for (const { a, b } of B.get(bk) ?? []) {
        if (marks++ >= 300) break;
        const [mx, my] = toScreen(s.view, (a.x + b.x) / 2, (a.y + b.y) / 2);
        if (mx < 0 || my < 0 || mx > s.w || my > s.h) continue;
        ctx.globalAlpha = a2;
        ctx.beginPath(); ctx.arc(mx, my, 8, 0, Math.PI * 2); ctx.fillStyle = P.bg; ctx.fill();
        ctx.lineWidth = 1.25; ctx.strokeStyle = col; ctx.stroke();
        ctx.fillStyle = col; ctx.fillText('!', mx, my + 0.5);
      }
    }
  }
  ctx.globalAlpha = 1;
  return n;
}

/** The on-screen radius of a service dot. */
export const dotRadius = (k: number): number => Math.max(1.3, Math.min(6, NODE_R * k * 0.9));

function drawDots(ctx: CanvasRenderingContext2D, s: Scene, a1: number, inView: (x: number, y: number) => boolean): number {
  const P = s.palette, rpx = dotRadius(s.view.k);
  const fills = new Map<string, PlacedNode[]>();
  const ghosts: PlacedNode[] = [], dim: PlacedNode[] = [];
  const hollow: [PlacedNode[], string, readonly number[]][] = [[[], P.neutral, NODE_DASH.inferred], [[], P.text2, []], [[], P.info, NODE_DASH.expectedOnly]];
  const errors: PlacedNode[] = [], warnings: PlacedNode[] = [];
  let n = 0;
  for (const p of s.nodes) {
    if (!p.vis || !inView(p.x, p.y)) continue;
    n++;
    if (p.vis === 2) { ghosts.push(p); continue; }
    if (isDim(s, p)) { dim.push(p); continue; }
    if (p.node.inferred) { hollow[0][0].push(p); continue; }
    if (p.node.external) { hollow[1][0].push(p); continue; }
    if (s.lens === 'both' && p.node.match === 'expected-only') { hollow[2][0].push(p); continue; }
    // Only what needs attention is coloured.
    const col = p.node.severity === 'error' ? P.err : p.node.severity === 'warning' ? P.warn : P.text3;
    if (p.node.severity === 'error') errors.push(p);
    if (p.node.severity === 'warning') warnings.push(p);
    const a = fills.get(col);
    if (a) a.push(p); else fills.set(col, [p]);
  }
  const path = (list: PlacedNode[], r: number) => {
    ctx.beginPath();
    for (const p of list) {
      const [sx, sy] = toScreen(s.view, p.x, p.y);
      if (r < 2) ctx.rect(sx - r, sy - r, r * 2, r * 2);
      else { ctx.moveTo(sx + r, sy); ctx.arc(sx, sy, r, 0, Math.PI * 2); }
    }
  };
  // Grey context: what the mode or the environment picked does not emphasise.
  if (dim.length) { ctx.globalAlpha = a1 * 0.3; ctx.fillStyle = P.text3; path(dim, rpx * 0.8); ctx.fill(); }
  if (ghosts.length) {
    ctx.globalAlpha = a1 * 0.45; ctx.strokeStyle = P.neutral; ctx.lineWidth = 1;
    ctx.setLineDash(rpx > 2.5 ? NODE_DASH.insufficient : []);
    path(ghosts, rpx); ctx.stroke(); ctx.setLineDash([]);
  }
  ctx.globalAlpha = a1;
  for (const [col, list] of fills) { ctx.fillStyle = col; path(list, rpx); ctx.fill(); }
  // Inferred, external and declared-with-nothing-running services are hollow rings, each with its own dash.
  ctx.lineWidth = 1.25; ctx.fillStyle = P.bg;
  for (const [list, col, dash] of hollow) {
    if (!list.length) continue;
    ctx.beginPath();
    for (const p of list) { const [sx, sy] = toScreen(s.view, p.x, p.y); ctx.moveTo(sx + rpx, sy); ctx.arc(sx, sy, rpx, 0, Math.PI * 2); }
    ctx.strokeStyle = col;
    ctx.setLineDash(rpx > 2.5 ? [...dash] : []);
    ctx.fill(); ctx.stroke(); ctx.setLineDash([]);
  }
  // An error is a double ring with a soft glow, a warning a single ring: never colour alone.
  for (const p of errors) {
    const [sx, sy] = toScreen(s.view, p.x, p.y);
    ctx.beginPath(); ctx.arc(sx, sy, rpx + 5, 0, Math.PI * 2); ctx.fillStyle = P.errBg; ctx.fill();
    ctx.lineWidth = 1.25; ctx.strokeStyle = P.err;
    ctx.beginPath(); ctx.arc(sx, sy, rpx + 2.5, 0, Math.PI * 2); ctx.stroke();
    ctx.beginPath(); ctx.arc(sx, sy, rpx + 5, 0, Math.PI * 2); ctx.stroke();
  }
  if (warnings.length) {
    ctx.lineWidth = 1.25; ctx.strokeStyle = P.warn;
    ctx.beginPath();
    for (const p of warnings) { const [sx, sy] = toScreen(s.view, p.x, p.y); ctx.moveTo(sx + rpx + 2.5, sy); ctx.arc(sx, sy, rpx + 2.5, 0, Math.PI * 2); }
    ctx.stroke();
  }
  ctx.globalAlpha = 1;
  return n;
}

/** clip shortens s with an ellipsis until it fits max pixels. */
function clip(ctx: CanvasRenderingContext2D, s: string, font: string, max: number): string {
  if (textW(ctx, s, font) <= max) return s;
  let t = s;
  while (t.length > 3 && textW(ctx, `${t}…`, font) > max) t = t.slice(0, -1);
  return `${t}…`;
}

/** labelNode names a service with its version beside its dot, or omits it when no side is free. */
function labelNode(ctx: CanvasRenderingContext2D, s: Scene, p: PlacedNode, alpha: number, rpx: number, occ: Occ, halo: Halo): boolean {
  const P = s.palette, n = p.node;
  const [sx, sy] = toScreen(s.view, p.x, p.y);
  const nf = `500 ${MIN_FONT}px ${P.sans}`, vf = `400 ${MIN_FONT}px ${P.sans}`, gf = `700 ${MIN_FONT}px ${P.sans}`;
  const glyph = n.severity === 'error' || n.severity === 'warning' ? SEVERITY_GLYPH[n.severity] : '';
  const name = n.label || n.name, ver = n.version ?? '';
  const wg = glyph ? textW(ctx, glyph, gf) + 4 : 0, w1 = textW(ctx, name, nf), w2 = ver ? textW(ctx, ver, vf) + 6 : 0;
  const w = wg + w1 + w2, h = 16, r0 = rpx + (glyph ? 6 : 0);
  const cands: [number, number][] = [[sx + r0 + 11, sy - h / 2], [sx - r0 - 11 - w, sy - h / 2], [sx - w / 2, sy - r0 - 7 - h], [sx - w / 2, sy + r0 + 7]];
  for (const [x, y] of cands) {
    // Generous padding: omit rather than squeeze.
    if (!occ.tryAdd([x - 8, y - 4, x + w + 8, y + h + 4])) continue;
    const dim = isDim(s, p);
    if (glyph) halo(glyph, x, y + h / 2, MIN_FONT, gf, n.severity === 'error' ? P.err : P.warn, alpha, 'left');
    halo(name, x + wg, y + h / 2, MIN_FONT, nf, dim ? P.text3 : n.inferred ? P.text2 : P.text, alpha * (dim ? 0.6 : 1), 'left');
    if (ver) halo(ver, x + wg + w1 + 6, y + h / 2, MIN_FONT, vf, P.text3, alpha, 'left');
    return true;
  }
  return false;
}

/** What Pacto knows of a declared service, by its match state. */
const KNOWN: Record<string, string> = { matched: 'observed', 'expected-only': 'not running', 'observed-only': 'observed, not declared', insufficient: 'no evidence' };

/** drawCard draws a service's card beside its dot: name, version and owner, interfaces and what Pacto knows, worst attention item. */
function drawCard(ctx: CanvasRenderingContext2D, s: Scene, p: PlacedNode, occ: Occ, dots: Occ, alpha: number, halo: Halo): boolean {
  const P = s.palette, n = p.node;
  const [sx, sy] = toScreen(s.view, p.x, p.y);
  const nf = `600 ${MIN_FONT}px ${P.sans}`, mf = `400 ${MIN_FONT}px ${P.sans}`;
  const owner = n.external ? 'external system' : n.owner || 'no owner';
  const l2 = [n.version ? `v${n.version}` : '', owner].filter(Boolean).join(' · ');
  const known = n.external ? 'outside Pacto' : n.inferred ? 'inferred, no contract' : KNOWN[n.match ?? ''] ?? 'no evidence';
  const l3 = n.external || n.inferred ? known : `${countOf(n.interfaces ?? 0, 'interface')} · ${known}`;
  const sev = n.severity === 'error' || n.severity === 'warning' ? n.severity : null;
  const l4 = sev && n.worst ? `${SEVERITY_GLYPH[sev]} ${attentionCategoryLabel(n.worst)}` : '';
  const name = n.label || n.name;
  const w = Math.min(220, Math.max(...[name, l2, l3, l4].map((t, i) => (t ? textW(ctx, t, i ? mf : nf) : 0))) + 22), h = l4 ? 72 : 56;
  const y = sy - h / 2;
  let x: number | null = null;
  // The dot sits on the card's left edge, or on its right edge when the right side is taken.
  for (const cx of [sx - 8, sx - w + 8]) {
    const r: Rect = [cx - 2, y - 2, cx + w + 2, y + h + 2];
    if (dots.count(r) <= 1 && occ.tryAdd(r)) { x = cx; break; }
  }
  if (x === null) return false;
  const dim = isDim(s, p);
  ctx.globalAlpha = alpha * (dim ? 0.5 : 1);
  ctx.beginPath(); ctx.roundRect(x, y, w, h, 6); ctx.fillStyle = P.bg; ctx.fill();
  ctx.lineWidth = sev === 'error' ? 2 : 1;
  ctx.strokeStyle = sev === 'error' ? P.err : sev === 'warning' ? P.warn : P.borderStrong;
  ctx.setLineDash(n.inferred || n.match === 'expected-only' ? [4, 3] : []);
  ctx.stroke(); ctx.setLineDash([]);
  const max = w - 18, a = alpha * (dim ? 0.5 : 1);
  halo(clip(ctx, name, nf, max), x + 12, y + 14, MIN_FONT, nf, dim ? P.text3 : P.text, a, 'left');
  halo(clip(ctx, l2, mf, max), x + 12, y + 30, MIN_FONT, mf, P.text2, a, 'left');
  halo(clip(ctx, l3, mf, max), x + 12, y + 46, MIN_FONT, mf, n.inferred || n.match !== 'matched' ? P.neutral : P.text3, a, 'left');
  if (l4) halo(clip(ctx, l4, mf, max), x + 12, y + 62, MIN_FONT, mf, sev === 'error' ? P.err : P.warn, a, 'left');
  ctx.globalAlpha = 1;
  return true;
}

const compact = (n: number) => (n > 999 ? `${(n / 1000).toFixed(1)}K` : String(n));

function pill(ctx: CanvasRenderingContext2D, s: Scene, x: number, y: number, n: number, sev: 'error' | 'warning', alpha: number, occ: Occ, labels: Halo): number {
  const P = s.palette, col = sev === 'error' ? P.err : P.warn;
  const txt = `${SEVERITY_GLYPH[sev]} ${compact(n)}`, font = `600 ${MIN_FONT}px ${P.sans}`;
  const w = Math.max(24, textW(ctx, txt, font) + 10), h = 18;
  if (!occ.tryAdd([x - 1, y - h / 2 - 1, x + w + 1, y + h / 2 + 1])) return 0;
  ctx.globalAlpha = alpha;
  ctx.beginPath(); ctx.roundRect(x, y - h / 2, w, h, h / 2);
  ctx.fillStyle = P.bg; ctx.fill(); ctx.lineWidth = 1.25; ctx.strokeStyle = col; ctx.stroke();
  labels(txt, x + w / 2, y + 0.5, MIN_FONT, font, col, alpha, 'center');
  return w;
}

/** outsideBadge draws a node's count of resources outside the contract at its top-right
 *  corner, in the neutral outline of the attention pills: no new colour, no ring change. */
function outsideBadge(ctx: CanvasRenderingContext2D, s: Scene, p: PlacedNode, rpx: number, alpha: number, occ: Occ, labels: Halo) {
  const P = s.palette;
  const [sx, sy] = toScreen(s.view, p.x, p.y);
  const txt = compact(p.node.outsideContract ?? 0), font = `600 ${MIN_FONT}px ${P.sans}`;
  const w = Math.max(18, textW(ctx, txt, font) + 8), h = 16;
  // Just clear of the space the dot claims (rpx + 1), at its top-right corner.
  const x = sx + rpx + 2, y = sy - rpx - h / 2;
  // ponytail: a badge with no free corner is omitted like a name; the selection panel and the group tooltip still count it.
  if (!occ.tryAdd([x - 1, y - h / 2 - 1, x + w + 1, y + h / 2 + 1])) return;
  ctx.globalAlpha = alpha;
  ctx.beginPath(); ctx.roundRect(x, y - h / 2, w, h, h / 2);
  ctx.fillStyle = P.bg; ctx.fill(); ctx.lineWidth = 1; ctx.strokeStyle = P.text2; ctx.setLineDash([]); ctx.stroke();
  labels(txt, x + w / 2, y + 0.5, MIN_FONT, font, P.text2, alpha, 'center');
}

function groupLabel(ctx: CanvasRenderingContext2D, s: Scene, gr: MapGroup, al0: number, occ: Occ, halo: Halo, dense: boolean) {
  const P = s.palette, m = s.mode ?? null;
  const [sx, sy] = toScreen(s.view, gr.x, gr.y);
  const R = gr.r * s.view.k;
  const special = isSpecial(gr);
  const al = al0 * (groupEmphasis(gr, m) ? 1 : 0.55);
  // Attention pills first, worst first: they win their space over the title. Gaps mode is about drift, not attention.
  let px = sx + R * 0.71, py = sy - R * 0.71 - 2;
  for (const [n, sev] of [[gr.attention.errors, 'error'], [gr.attention.warnings, 'warning']] as const) {
    if (!n || m?.kind === 'gaps' || (sev === 'warning' && gr.attention.errors && R < 30)) continue;
    const w = pill(ctx, s, px, py, n, sev, al, occ, halo);
    if (w) px += w + 3;
  }
  const fs = Math.max(MIN_FONT, Math.min(19, R * 0.3)), cs = Math.max(MIN_FONT, Math.round(fs * 0.7));
  const tf = `600 ${fs}px ${P.display}`, cf = `400 ${cs}px ${P.sans}`;
  const n = groupCount(gr, s.lens);
  const count = s.groupBy === 'environment' && !special ? `${formatCount(n)} running` : countOf(n, 'service');
  const pct = gr.match.percent;
  const third = (special ? null : groupModeLine(gr, m)) ?? (special ? '' : pct == null ? 'no evidence' : `${pct}% match`);
  const title = gr.label;
  // A two-word name wraps onto two lines before it gives up ("Previews (30)" stays whole).
  const sp = title.indexOf(' ');
  const tl: [string, string, number, string][] = sp > 0 && title[sp + 1] !== '(' && textW(ctx, title, tf) > R * 1.9
    ? [[title.slice(0, sp), tf, fs, P.text], [title.slice(sp + 1), tf, fs, P.text]]
    : [[title, tf, fs, P.text]];
  const lines = tl.concat([[count, cf, cs, P.text2]], third ? [[third, cf, cs, P.text2]] : []);
  const lh = lines.map(([, f]) => (f === tf ? fs * 1.15 : cs * 1.35));
  const nt = tl.length;
  // The full label inside, then shorter ones; below the bubble only when bubbles are few.
  const tries: [number, boolean][] = dense ? [[lines.length, true], [nt + 1, true], [nt, true]]
    : [[lines.length, true], [nt + 1, true], [nt, true], [lines.length, false], [nt, false]];
  for (const [nl, inside] of tries) {
    const ls = lines.slice(0, nl);
    const w = Math.max(...ls.map(([t, f]) => textW(ctx, t, f))), h = ls.reduce((a, _, i) => a + lh[i], 0);
    if (inside && !(w < R * 2.05 && h < R * 1.6)) continue;
    const y0 = inside ? sy - h / 2 : sy + R + 8;
    if (!occ.tryAdd([sx - w / 2 - 3, y0, sx + w / 2 + 3, y0 + h])) continue;
    let y = y0;
    ls.forEach(([t, f, size, c], i) => { halo(t, sx, y + lh[i] / 2, size, f, c, al, 'center'); y += lh[i]; });
    return;
  }
  const short = formatCount(n);
  const sw = textW(ctx, short, cf);
  if (R > 14 && occ.tryAdd([sx - sw / 2 - 3, sy - cs * 0.7, sx + sw / 2 + 3, sy + cs * 0.7])) halo(short, sx, sy, cs, cf, P.text2, al, 'center');
}

/** At the Services level a group keeps a caption over its rim: name, count and attention. */
function groupCaption(ctx: CanvasRenderingContext2D, s: Scene, gr: MapGroup, a1: number, occ: Occ, halo: Halo) {
  const P = s.palette;
  const [sx, sy] = toScreen(s.view, gr.x, gr.y);
  const R = gr.r * s.view.k;
  const tf = `600 ${MIN_FONT}px ${P.display}`, ef = `600 ${MIN_FONT}px ${P.sans}`;
  const err = gr.attention.errors, warn = gr.attention.warnings;
  const errTxt = err ? `${SEVERITY_GLYPH.error} ${compact(err)}` : '', warnTxt = warn ? `${SEVERITY_GLYPH.warning} ${compact(warn)}` : '';
  const txt = `${gr.label} · ${formatCount(groupCount(gr, s.lens))}`;
  const w = textW(ctx, txt, tf);
  const we = (errTxt ? textW(ctx, errTxt, ef) + 10 : 0) + (warnTxt ? textW(ctx, warnTxt, ef) + 10 : 0);
  const y = sy - R - 12;
  if (!occ.tryAdd([sx - (w + we) / 2 - 3, y - 10, sx + (w + we) / 2 + 3, y + 10])) return;
  let x = sx - (w + we) / 2;
  halo(txt, x, y, MIN_FONT, tf, P.text2, a1, 'left');
  x += w + 10;
  if (errTxt) { halo(errTxt, x, y, MIN_FONT, ef, P.err, a1, 'left'); x += textW(ctx, errTxt, ef) + 10; }
  if (warnTxt) halo(warnTxt, x, y, MIN_FONT, ef, P.warn, a1, 'left');
}

/** Sub-clusters (teams inside a domain past 40 teams, each preview) are named over their rim from the Services level on. */
function subLabels(ctx: CanvasRenderingContext2D, s: Scene, gr: MapGroup, a1: number, occ: Occ, halo: Halo) {
  const P = s.palette, f = `500 ${MIN_FONT}px ${P.sans}`;
  for (const c of gr.subs ?? []) {
    const [cx, cy] = toScreen(s.view, gr.x + c.x, gr.y + c.y);
    const t = c.label || c.key, w = textW(ctx, t, f), y = cy - c.r * s.view.k - 2;
    if (occ.tryAdd([cx - w / 2 - 6, y - 9, cx + w / 2 + 6, y + 9])) halo(t, cx, y, MIN_FONT, f, P.text3, a1, 'center');
  }
}

/** The minimap's transform: world point (x, y) sits at (x * s + ox, y * s + oy). */
export interface MiniTransform { s: number; ox: number; oy: number }

/** drawMinimap draws every group and the viewport's frame on the minimap. */
export function drawMinimap(ctx: CanvasRenderingContext2D, groups: readonly MapGroup[], b: { minX: number; minY: number; maxX: number; maxY: number },
  view: View, w: number, h: number, mw: number, mh: number, P: Palette): MiniTransform {
  const s = Math.min((mw - 12) / Math.max(1, b.maxX - b.minX), (mh - 12) / Math.max(1, b.maxY - b.minY));
  const ox = mw / 2 - ((b.minX + b.maxX) / 2) * s, oy = mh / 2 - ((b.minY + b.maxY) / 2) * s;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.globalAlpha = 1;
  ctx.fillStyle = P.bg; ctx.fillRect(0, 0, mw, mh);
  for (const g of groups) {
    ctx.beginPath(); ctx.arc(g.x * s + ox, g.y * s + oy, Math.max(1.5, g.r * s), 0, Math.PI * 2);
    ctx.fillStyle = P.raised; ctx.fill();
    ctx.strokeStyle = g.attention.errors ? P.err : P.borderStrong; ctx.lineWidth = 1; ctx.stroke();
  }
  const [x0, y0] = toWorld(view, 0, 0), [x1, y1] = toWorld(view, w, h);
  ctx.strokeStyle = P.accent; ctx.lineWidth = 1.5;
  ctx.strokeRect(x0 * s + ox, y0 * s + oy, (x1 - x0) * s, (y1 - y0) * s);
  return { s, ox, oy };
}
