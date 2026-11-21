/**
 * Draws the fleet map on a 2D canvas, ported from the approved mockup
 * (docs/superpowers/mockups/fleet-graph-semantic-zoom.html, rendering).
 *
 * Groups level: one neutral bubble per group with its match ring, attention
 * pills and a centred label, and one thin, low-contrast ribbon per group pair
 * split by how declared and observed agree. Services level: the bubbles fade
 * to containers and each service is a dot, neutral unless it needs attention.
 * The two levels crossfade over the threshold. Colour never stands alone: a
 * ring is solid for its matched share and dashed for the rest, a pill carries
 * a glyph and a count, an error dot a double ring.
 *
 * Every drawn label is recorded with its rectangle and font size, so a
 * browser test can check that none overlap, cross the edge or shrink below
 * the smallest type step.
 */
import { formatCount } from '../format';
import { groupCount, ribbonCount, type Lens, type MapGroup, type MapNode, type MapRibbon, type Visibility, nodeVisibility } from './lens';
import { L1, NODE_R, gapOf, smooth, toScreen, toWorld, type View } from './view';
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

export type Rect = [number, number, number, number];

/** One drawn label: its text, rectangle in CSS pixels and font size in pixels. */
export interface DrawnLabel { text: string; x: number; y: number; w: number; h: number; size: number }

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
  hoverGroup?: string | null;
  /** Screen areas the HUD covers: no label is drawn there. */
  reserved?: readonly Rect[];
}

export interface RenderResult { labels: DrawnLabel[]; nodes: number; ribbons: number }

/** The smallest text the map draws, in px: above the 12.19px floor of the type ramp. */
export const MIN_FONT = 13;

const SPECIAL = new Set(['external', 'unowned', 'none', 'unknown', 'other']);
export const isSpecial = (g: MapGroup): boolean => SPECIAL.has(g.kind);

/** Line dash per match state: matched solid, expected-only dashed, observed-only dotted, insufficient fine dots. */
export const EDGE_DASH = { matched: [] as number[], expectedOnly: [5, 4], observedOnly: [1.5, 3], insufficient: [1, 2] };

/** Glyphs that carry severity beside colour. */
export const SEVERITY_GLYPH = { error: '✕', warning: '▲', info: 'i' } as const;

/** Occupancy of screen rectangles, so labels never overlap and never cross the canvas edge. */
class Occ {
  private m = new Map<number, Rect[]>();
  constructor(private w: number, private h: number) {}
  private cells(r: Rect, f: (k: number) => void) {
    for (let x = Math.floor(r[0] / 64); x <= Math.floor(r[2] / 64); x++) for (let y = Math.floor(r[1] / 64); y <= Math.floor(r[3] / 64); y++) f(x * 100000 + y);
  }
  free(r: Rect): boolean {
    let hit = false;
    this.cells(r, (k) => { if (!hit) for (const q of this.m.get(k) ?? []) if (r[0] < q[2] && r[2] > q[0] && r[1] < q[3] && r[3] > q[1]) { hit = true; break; } });
    return !hit;
  }
  add(r: Rect) { this.cells(r, (k) => { const a = this.m.get(k); if (a) a.push(r); else this.m.set(k, [r]); }); }
  tryAdd(r: Rect): boolean {
    if (r[0] < 0 || r[1] < 0 || r[2] > this.w || r[3] > this.h || !this.free(r)) return false;
    this.add(r);
    return true;
  }
}

const widths = new Map<string, number>();
function textW(ctx: CanvasRenderingContext2D, s: string, font: string): number {
  const k = `${font}|${s}`;
  let w = widths.get(k);
  if (w === undefined) { ctx.font = font; w = ctx.measureText(s).width; widths.set(k, w); }
  return w;
}

const wOf = (n: number) => (n ? Math.min(5, 0.6 + Math.sqrt(n) * 0.35) : 0);

/** render draws one frame. */
export function render(ctx: CanvasRenderingContext2D, s: Scene): RenderResult {
  const P = s.palette, { k } = s.view, g = gapOf(k);
  const labels: DrawnLabel[] = [];
  ctx.setTransform(s.dpr, 0, 0, s.dpr, 0, 0);
  ctx.globalAlpha = 1;
  ctx.fillStyle = P.bg;
  ctx.fillRect(0, 0, s.w, s.h);
  const a1 = smooth(g, L1 * 0.88, L1 * 1.12);
  const [wx0, wy0] = toWorld(s.view, -40, -40), [wx1, wy1] = toWorld(s.view, s.w + 40, s.h + 40);
  const inView = (x: number, y: number, m = 0) => x > wx0 - m && x < wx1 + m && y > wy0 - m && y < wy1 + m;
  const byKey = new Map(s.groups.map((gr) => [gr.key, gr]));
  const hg = s.hoverGroup && a1 < 0.5 ? s.hoverGroup : null;

  const ribbons = drawRibbons(ctx, s, byKey, a1, hg);
  for (const gr of s.groups) if (inView(gr.x, gr.y, gr.r)) drawBubble(ctx, s, gr, a1, hg);
  const nodes = a1 > 0 ? drawDots(ctx, s, a1, inView) : 0;

  const occ = new Occ(s.w, s.h);
  for (const r of s.reserved ?? []) occ.add(r);
  const halo = (txt: string, x: number, y: number, size: number, font: string, color: string, alpha: number, align: CanvasTextAlign) => {
    ctx.font = font; ctx.globalAlpha = alpha; ctx.textAlign = align; ctx.textBaseline = 'middle';
    ctx.lineJoin = 'round'; ctx.lineWidth = 3; ctx.strokeStyle = P.bg; ctx.strokeText(txt, x, y);
    ctx.fillStyle = color; ctx.fillText(txt, x, y);
    const w = textW(ctx, txt, font);
    const left = align === 'center' ? x - w / 2 : x;
    labels.push({ text: txt, x: left, y: y - size * 0.6, w, h: size * 1.2, size });
  };
  // Dots claim their space first, so a label never sits on a service.
  if (a1 > 0.5) {
    const rpx = dotRadius(k);
    for (const p of s.nodes) if (p.vis && inView(p.x, p.y)) { const [sx, sy] = toScreen(s.view, p.x, p.y); occ.add([sx - rpx, sy - rpx, sx + rpx, sy + rpx]); }
  }
  const order = s.groups.filter((gr) => inView(gr.x, gr.y, gr.r)).sort((a, b) => b.services - a.services);
  for (const gr of order) {
    if (a1 < 1) groupLabel(ctx, s, gr, 1 - a1, occ, halo, s.groups.length > 40);
    if (a1 > 0) groupCaption(ctx, s, gr, a1, occ, halo);
  }
  ctx.globalAlpha = 1;
  ctx.setLineDash([]);
  return { labels, nodes, ribbons };
}

type Halo = (txt: string, x: number, y: number, size: number, font: string, color: string, alpha: number, align: CanvasTextAlign) => void;

function drawRibbons(ctx: CanvasRenderingContext2D, s: Scene, byKey: Map<string, MapGroup>, a1: number, hg: string | null): number {
  const P = s.palette, k = s.view.k;
  // Ribbons are the Groups level's edges; at the Services level they stay as faint bundles.
  const al = a1 < 1 ? 1 - a1 * 0.4 : 0.6;
  let drawn = 0;
  const curve = (A: MapGroup, B: MapGroup, off: number, w: number, color: string, alpha: number, dash: number[]) => {
    const [ax, ay] = toScreen(s.view, A.x, A.y), [bx, by] = toScreen(s.view, B.x, B.y);
    const dx = bx - ax, dy = by - ay, len = Math.hypot(dx, dy) || 1, ux = dx / len, uy = dy / len, nx = -uy, ny = ux;
    const p0x = ax + ux * (A.r * k + 6) + nx * off, p0y = ay + uy * (A.r * k + 6) + ny * off;
    const p1x = bx - ux * (B.r * k + 6) + nx * off, p1y = by - uy * (B.r * k + 6) + ny * off;
    const bend = Math.min(40, len * 0.08);
    ctx.beginPath();
    ctx.moveTo(p0x, p0y);
    ctx.quadraticCurveTo((p0x + p1x) / 2 + nx * bend, (p0y + p1y) / 2 + ny * bend, p1x, p1y);
    // ponytail: the canvas dashes the whole path, off-screen part included; very long dashed curves draw solid.
    ctx.setLineDash(dash.length && len > Math.max(s.w, s.h) * 1.5 ? [] : dash);
    ctx.lineWidth = w; ctx.strokeStyle = color; ctx.globalAlpha = al * alpha;
    ctx.stroke();
  };
  const baseA = P.dark ? 0.22 : 0.3;
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
    if (r.crossEnvironment) { curve(A, B, 0, Math.max(1.5, wOf(r.crossEnvironment)), P.err, 0.9, [6, 4]); continue; }
    if (s.lens !== 'both') { curve(A, B, 0, wOf(n), P.text3, dim ? 0.08 : baseA, []); continue; }
    // Both: side-by-side strands by how declared and observed agree.
    const strands = ([
      ['matched', r.matched, P.text3, EDGE_DASH.matched],
      ['expectedOnly', r.expectedOnly, P.info, EDGE_DASH.expectedOnly],
      ['observedOnly', r.observedOnly, P.warn, EDGE_DASH.observedOnly],
      ['insufficient', r.insufficient, P.neutral, EDGE_DASH.insufficient],
    ] as const).filter(([, c]) => c);
    const ws = strands.map(([kk, c]) => Math.max(1, wOf(c ?? 0) * (kk === 'matched' ? 1 : 0.8)));
    let off = -ws.reduce((a, b) => a + b + 1.5, 0) / 2;
    strands.forEach(([kk, , col, dash], i) => {
      off += ws[i] / 2;
      curve(A, B, off, ws[i], col, dim ? 0.08 : kk === 'matched' ? baseA : 0.5, [...dash]);
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
  ctx.beginPath();
  ctx.arc(sx, sy, R, 0, Math.PI * 2);
  ctx.globalAlpha = 1 - 0.6 * a1;
  ctx.fillStyle = special ? P.bg : P.raised;
  ctx.fill();
  ctx.globalAlpha = 1;
  ctx.lineWidth = hg === gr.key ? 2 : 1;
  ctx.strokeStyle = hg === gr.key ? P.accent : P.borderStrong;
  ctx.setLineDash(special ? [4, 4] : []);
  ctx.stroke();
  ctx.setLineDash([]);
  if (a1 >= 1 || special || R <= 10) return;
  // The match ring: dashed all round, solid over the matched share (not colour alone).
  const rr = R + 3.5;
  ctx.globalAlpha = 1 - a1;
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

/** The on-screen radius of a service dot. */
export const dotRadius = (k: number): number => Math.max(1.3, Math.min(6, NODE_R * k * 0.9));

function drawDots(ctx: CanvasRenderingContext2D, s: Scene, a1: number, inView: (x: number, y: number) => boolean): number {
  const P = s.palette, rpx = dotRadius(s.view.k);
  const fills = new Map<string, PlacedNode[]>();
  const ghosts: PlacedNode[] = [], hollow: [PlacedNode[], string, boolean][] = [[[], P.neutral, true], [[], P.text2, false], [[], P.info, true]];
  const errors: PlacedNode[] = [];
  let n = 0;
  for (const p of s.nodes) {
    if (!p.vis || !inView(p.x, p.y)) continue;
    n++;
    if (p.vis === 2) { ghosts.push(p); continue; }
    if (p.node.inferred) { hollow[0][0].push(p); continue; }
    if (p.node.external) { hollow[1][0].push(p); continue; }
    if (s.lens === 'both' && p.node.match === 'expected-only') { hollow[2][0].push(p); continue; }
    // Only what needs attention is coloured.
    const col = p.node.severity === 'error' ? P.err : p.node.severity === 'warning' ? P.warn : P.text3;
    if (p.node.severity === 'error') errors.push(p);
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
  ctx.globalAlpha = a1 * 0.45; ctx.strokeStyle = P.neutral; ctx.lineWidth = 1;
  path(ghosts, rpx); ctx.stroke();
  ctx.globalAlpha = a1;
  for (const [col, list] of fills) { ctx.fillStyle = col; path(list, rpx); ctx.fill(); }
  // Inferred: dashed neutral ring; external: solid ring; declared with nothing running: dashed info ring.
  ctx.lineWidth = 1.25; ctx.fillStyle = P.bg;
  for (const [list, col, dashed] of hollow) {
    if (!list.length) continue;
    ctx.beginPath();
    for (const p of list) { const [sx, sy] = toScreen(s.view, p.x, p.y); ctx.moveTo(sx + rpx, sy); ctx.arc(sx, sy, rpx, 0, Math.PI * 2); }
    ctx.strokeStyle = col;
    ctx.setLineDash(dashed && rpx > 2.5 ? [2, 2] : []);
    ctx.fill(); ctx.stroke(); ctx.setLineDash([]);
  }
  // An error is a double ring with a soft glow, so it never reads by colour alone.
  for (const p of errors) {
    const [sx, sy] = toScreen(s.view, p.x, p.y);
    ctx.beginPath(); ctx.arc(sx, sy, rpx + 5, 0, Math.PI * 2); ctx.fillStyle = P.errBg; ctx.fill();
    ctx.lineWidth = 1.25; ctx.strokeStyle = P.err;
    ctx.beginPath(); ctx.arc(sx, sy, rpx + 2.5, 0, Math.PI * 2); ctx.stroke();
    ctx.beginPath(); ctx.arc(sx, sy, rpx + 5, 0, Math.PI * 2); ctx.stroke();
  }
  ctx.globalAlpha = 1;
  return n;
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

function groupLabel(ctx: CanvasRenderingContext2D, s: Scene, gr: MapGroup, al: number, occ: Occ, halo: Halo, dense: boolean) {
  const P = s.palette;
  const [sx, sy] = toScreen(s.view, gr.x, gr.y);
  const R = gr.r * s.view.k;
  const special = isSpecial(gr);
  // Attention pills first, worst first: they win their space over the title.
  let px = sx + R * 0.71, py = sy - R * 0.71 - 2;
  for (const [n, sev] of [[gr.attention.errors, 'error'], [gr.attention.warnings, 'warning']] as const) {
    if (!n || (sev === 'warning' && gr.attention.errors && R < 30)) continue;
    const w = pill(ctx, s, px, py, n, sev, al, occ, halo);
    if (w) px += w + 3;
  }
  const fs = Math.max(MIN_FONT, Math.min(19, R * 0.3)), cs = Math.max(MIN_FONT, Math.round(fs * 0.7));
  const tf = `600 ${fs}px ${P.display}`, cf = `400 ${cs}px ${P.sans}`;
  const n = groupCount(gr, s.lens);
  const count = s.groupBy === 'environment' && !special ? `${formatCount(n)} running` : `${formatCount(n)} ${n === 1 ? 'service' : 'services'}`;
  const pct = gr.match.percent;
  const third = special ? '' : pct == null ? 'no evidence' : `${pct}% match`;
  const title = gr.label;
  // A two-word name wraps onto two lines before it gives up.
  const sp = title.indexOf(' ');
  const tl: [string, string, number, string][] = sp > 0 && textW(ctx, title, tf) > R * 1.9
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
