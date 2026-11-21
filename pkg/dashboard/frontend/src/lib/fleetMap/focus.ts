/**
 * The fleet map's focus diagram: one service and its neighbourhood, left to
 * right, ported from the approved mockup
 * (docs/superpowers/mockups/fleet-graph-semantic-zoom.html, focus mode).
 *
 * Callers sit in the column on the left ("Calls <service>"), callees on the
 * right ("<service> calls"), at most nine a column, worst first, with "N of M
 * shown" when there are more. One more hop adds their callers and callees,
 * expanded from the shown nodes only (the server's answer). Edges run only
 * between adjacent columns in the call direction, as a bezier with an
 * arrowhead; each is labelled with the interface and the range it accepts, and
 * a problem edge with the problem's reason instead, which is the only place an
 * operation appears. Ports spread along a card's edge in the order of the
 * cards at the other end, so arrowheads never pile up.
 *
 * Geometry is laid out in diagram units (screen px at zoom 1) and drawn in
 * screen space, so text keeps its size at every zoom and a line that does
 * not fit a card is omitted rather than squeezed.
 */
import { countOf, statusTone } from '../format';
import { formatDateTime } from '../dateFormat';
import { attentionCause, attentionCategoryLabel, provenanceLabel } from '../entityLabels';
import { differenceLabel } from '../graphState';
import type { FleetMapFocusAnswer } from '../api';
import { EDGE_DASH, MIN_FONT, SEVERITY_GLYPH, type DrawnLabel, type Palette, type Rect } from './render';
import { FIT_MARGIN, toScreen, viewFor, type View, type Viewport } from './view';

export type FocusAnswer = FleetMapFocusAnswer;
export type FocusNode = FocusAnswer['focus'];
export type FocusEdge = NonNullable<FocusAnswer['edges']>[number];

/** Card sizes, column gap, row pitch and nodes a column (mockup FC). */
export const FC = { SW: 176, SH: 62, CW: 264, CH: 108, G: 196, ROW: 76, MAXR: 9 } as const;

/** A card in the diagram: its node, column (0 the focus) and rectangle [x, y, w, h] in diagram units. */
export interface FocusCard { node: FocusNode; col: number; rect: [number, number, number, number] }
export interface FocusColumn { hop: number; x: number; top: number; n: number; total: number }

export interface FocusLayout {
  focus: FocusNode;
  /** The focus service's group in the domain grouping. */
  group: string;
  hops: number;
  asOf: string;
  cards: FocusCard[];
  columns: FocusColumn[];
  /** Only edges between adjacent columns in the call direction. */
  edges: FocusEdge[];
  byKey: Map<string, FocusCard>;
  /** How many services call the focus and how many it calls, shown or not. */
  callers: number;
  callees: number;
}

const COL_X: Record<number, number> = {
  [-2]: -FC.CW / 2 - 2 * (FC.G + FC.SW),
  [-1]: -FC.CW / 2 - FC.G - FC.SW,
  1: FC.CW / 2 + FC.G,
  2: FC.CW / 2 + 2 * FC.G + FC.SW,
};

const sevRank = (n: FocusNode) => (n.severity === 'error' ? 2 : n.severity === 'warning' ? 1 : 0);
/** worstFirst orders nodes by severity, then name. */
export const worstFirst = (a: FocusNode, b: FocusNode): number => sevRank(b) - sevRank(a) || a.name.localeCompare(b.name);

/** layoutFocus places the focus answer's columns and keeps the edges that follow the flow. */
export function layoutFocus(a: FocusAnswer): FocusLayout {
  const cards: FocusCard[] = [{ node: a.focus, col: 0, rect: [-FC.CW / 2, -FC.CH / 2, FC.CW, FC.CH] }];
  const columns: FocusColumn[] = [];
  const byKey = new Map<string, FocusCard>([[a.focus.key, cards[0]]]);
  for (const c of [...(a.columns ?? [])].sort((x, y) => x.hop - y.hop)) {
    const list = [...(c.nodes ?? [])].sort(worstFirst).slice(0, FC.MAXR);
    const n = list.length, x = COL_X[c.hop];
    if (x === undefined) continue;
    const top = -((n - 1) / 2) * FC.ROW - FC.SH / 2;
    columns.push({ hop: c.hop, x, top, n, total: Math.max(c.total, n) });
    list.forEach((node, i) => {
      if (byKey.has(node.key)) return;
      const card: FocusCard = { node, col: c.hop, rect: [x, top + i * FC.ROW, FC.SW, FC.SH] };
      cards.push(card);
      byKey.set(node.key, card);
    });
  }
  const colOf = (k: string) => byKey.get(k)?.col;
  const edges = (a.edges ?? []).filter((e) => {
    const cf = colOf(e.from.key), ct = colOf(e.to.key);
    return cf !== undefined && ct === cf + 1;
  });
  const total = (hop: number) => columns.find((c) => c.hop === hop)?.total ?? 0;
  return { focus: a.focus, group: a.group, hops: a.hops, asOf: a.meta?.asOf ?? '', cards, columns, edges, byKey, callers: total(-1), callees: total(1) };
}

/** columnTitle is a column's header, in plain words. */
export function columnTitle(hop: number, name: string): string {
  return hop === -1 ? `Calls ${name}` : hop === 1 ? `${name} calls` : hop === -2 ? 'Their callers' : 'What those call';
}

/** columnCount is a column's sub-header: how many it shows of how many. */
export const columnCount = (c: FocusColumn): string => (c.total > c.n ? `${c.n} of ${c.total} shown` : countOf(c.total, 'service'));

/** An edge's label and how bad it is: 0 none, 1 warning, 2 error. */
export interface EdgeLabel { text: string; sev: 0 | 1 | 2 }

const join = (...parts: (string | undefined)[]) => parts.filter(Boolean).join(' · ');

/**
 * edgeLabel is the interface and the range a dependency accepts; a problem
 * edge reads its problem instead (the reason names the operation, if any).
 */
export function edgeLabel(e: FocusEdge): EdgeLabel {
  if (e.problem) return { text: e.problem.reason || attentionCause(e.problem.code), sev: e.problem.severity === 'error' ? 2 : 1 };
  if (e.inferred) return { text: join(e.interface, 'inferred'), sev: 0 };
  if (!e.expected) return { text: join(e.interface, 'not declared'), sev: 1 };
  return { text: [e.interface, e.range].filter(Boolean).join(' '), sev: 0 };
}

/** An edge's tooltip: caller → callee, its problem, then one line per fact. */
export interface EdgeTip { title: string; problem?: EdgeLabel; lines: string[] }

/** edgeTip lists the edge's provenance, range and running version, declaring contract, evidence source and asOf. */
export function edgeTip(e: FocusEdge, asOf: string): EdgeTip {
  const src = e.observationSources?.items?.map((s) => s.source) ?? [];
  const more = (e.observationSources?.total ?? src.length) - src.length;
  const evidence = src.length ? src.join(', ') + (more > 0 ? ` and ${more} more` : '')
    : e.observed ? 'observed' : 'no evidence source covers this call';
  const diff = e.inferred ? differenceLabel('inferred') : e.difference ? differenceLabel(e.difference) : '';
  const lines = [
    `Provenance: ${provenanceLabel(e.provenance)}${diff && diff !== provenanceLabel(e.provenance) ? ` (${diff.toLowerCase()})` : ''}`,
    `Accepts: ${e.range || 'any version'}${e.runningVersion ? ` · runs ${e.runningVersion}` : ''}`,
    `Declared in: ${e.declaredIn ? `contract ${e.declaredIn.label}` : 'not declared'}`,
    `Evidence: ${evidence}${e.stale ? ' (stale)' : ''}${e.environmentUnknown ? ' · environment unknown' : ''}`,
  ];
  if (e.lastSeen) lines.push(`Last seen ${formatDateTime(e.lastSeen)}`);
  lines.push(`As of ${formatDateTime(asOf)}`);
  const lab = edgeLabel(e);
  return { title: `${e.from.label} → ${e.to.label}`, problem: e.problem ? lab : undefined, lines };
}

/** focusBounds is the diagram's extent, with room for the headers above and the legend below. */
function focusBounds(L: FocusLayout): Rect {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const { rect: [x, y, w, h] } of L.cards) { x0 = Math.min(x0, x); y0 = Math.min(y0, y); x1 = Math.max(x1, x + w); y1 = Math.max(y1, y + h); }
  return [x0, y0 - 52, x1, y1 + 40];
}

/** focusView frames the diagram in the usable canvas, never past zoom 1.15. */
/** The smallest focus zoom at which a card still has room for its name. */
export const FOCUS_READABLE = 0.45;

export function focusView(L: FocusLayout, vp: Viewport): View {
  const [x0, y0, x1, y1] = focusBounds(L);
  const usable = Math.max(1, vp.w - (vp.rail ?? 0));
  const z = Math.min(1.15, (usable - 2 * FIT_MARGIN) / (x1 - x0), (vp.h - 2 * FIT_MARGIN) / (y1 - y0));
  // Too wide to fit with its names readable (a phone, two hops): the focus card at the
  // smallest zoom that still names every card, the columns a pan away.
  if (z < FOCUS_READABLE) return viewFor(vp, 0, 0, FOCUS_READABLE);
  return viewFor(vp, (x0 + x1) / 2, (y0 + y1) / 2, z);
}

/** screenRects puts every card on screen. */
export function screenRects(L: FocusLayout, v: View): Map<string, Rect> {
  const m = new Map<string, Rect>();
  for (const c of L.cards) {
    const [x, y, w, h] = c.rect;
    const [sx, sy] = toScreen(v, x, y);
    m.set(c.node.key, [sx, sy, sx + w * v.k, sy + h * v.k]);
  }
  return m;
}

/** A cubic bezier: start, two controls, end. */
export type Geo = [number, number, number, number, number, number, number, number];

/** edgeGeometry spreads each card's ports in the order of the cards at the other end and draws each edge as a bezier. */
export function edgeGeometry(edges: readonly FocusEdge[], rects: Map<string, Rect>): Map<string, Geo> {
  const outs = new Map<string, FocusEdge[]>(), ins = new Map<string, FocusEdge[]>();
  const push = (m: Map<string, FocusEdge[]>, k: string, e: FocusEdge) => { const a = m.get(k); if (a) a.push(e); else m.set(k, [e]); };
  for (const e of edges) if (rects.has(e.from.key) && rects.has(e.to.key)) { push(outs, e.from.key, e); push(ins, e.to.key, e); }
  const sy = new Map<string, number>(), ty = new Map<string, number>();
  const spread = (m: Map<string, FocusEdge[]>, into: Map<string, number>, other: (e: FocusEdge) => string) => {
    for (const [k, list] of m) {
      const r = rects.get(k)!, h = r[3] - r[1], pad = Math.min(14, h / 4);
      list.sort((a, b) => rects.get(other(a))![1] - rects.get(other(b))![1]);
      list.forEach((e, i) => into.set(e.id, list.length === 1 ? r[1] + h / 2 : r[1] + pad + (i * (h - 2 * pad)) / (list.length - 1)));
    }
  };
  spread(outs, sy, (e) => e.to.key);
  spread(ins, ty, (e) => e.from.key);
  const geo = new Map<string, Geo>();
  for (const e of edges) {
    const a = rects.get(e.from.key), b = rects.get(e.to.key);
    if (!a || !b) continue;
    const x0 = a[2], x3 = b[0], y0 = sy.get(e.id)!, y3 = ty.get(e.id)!;
    const dx = Math.max(40, Math.abs(x3 - x0) * 0.5);
    geo.set(e.id, [x0, y0, x0 + dx, y0, x3 - dx, y3, x3, y3]);
  }
  return geo;
}

/** bez is the point at t on a bezier. */
export function bez(g: Geo, t: number): [number, number] {
  const u = 1 - t;
  return [
    u * u * u * g[0] + 3 * u * u * t * g[2] + 3 * u * t * t * g[4] + t * t * t * g[6],
    u * u * u * g[1] + 3 * u * u * t * g[3] + 3 * u * t * t * g[5] + t * t * t * g[7],
  ];
}

/** edgeDash is the line style of an edge's match state. */
export function edgeDash(e: FocusEdge): readonly number[] {
  if (e.problem?.code === 'CROSS_ENVIRONMENT_CALL' || e.problem?.code === 'CROSS_ENVIRONMENT_PREVIEW_CALL') return EDGE_DASH.crossEnvironment;
  if (e.inferred || e.difference === 'observed-not-expected') return EDGE_DASH.observedOnly;
  if (e.difference === 'expected-not-observed') return EDGE_DASH.expectedOnly;
  if (e.difference === 'insufficient') return EDGE_DASH.insufficient;
  return EDGE_DASH.matched;
}

/** The focus legend: how to read the diagram, beside colour. */
export const FOCUS_LEGEND: readonly { label: string; dash?: readonly number[]; mark?: string }[] = [
  { label: 'calls (depends on)', mark: '→' },
  { label: 'declared and seen', dash: EDGE_DASH.matched },
  { label: 'declared, not seen', dash: EDGE_DASH.expectedOnly },
  { label: 'seen, not declared', dash: EDGE_DASH.observedOnly },
  { label: 'problem', mark: '!' },
];

export interface FocusScene {
  view: View;
  w: number;
  h: number;
  dpr: number;
  palette: Palette;
  /** Progress of the entry fade, 0 to 1. */
  t: number;
  hoverEdge?: string | null;
  hoverNode?: string | null;
}

export interface FocusFrame { labels: DrawnLabel[]; rects: Map<string, Rect>; geo: Map<string, Geo> }

const free = (taken: Rect[], r: Rect) => taken.every((q) => r[0] >= q[2] || r[2] <= q[0] || r[1] >= q[3] || r[3] <= q[1]);
const KNOWN: Record<string, string> = { matched: 'declared and observed', 'expected-only': 'declared, not running', 'observed-only': 'observed, not declared', insufficient: 'declared, no evidence' };

/** renderFocus draws the focus diagram; it returns every drawn label and the geometry hit testing reads. */
export function renderFocus(ctx: CanvasRenderingContext2D, L: FocusLayout, s: FocusScene): FocusFrame {
  const P = s.palette, labels: DrawnLabel[] = [];
  ctx.setTransform(s.dpr, 0, 0, s.dpr, 0, 0);
  ctx.globalAlpha = 1;
  ctx.fillStyle = P.bg;
  ctx.fillRect(0, 0, s.w, s.h);
  const rects = screenRects(L, s.view);
  const geo = edgeGeometry(L.edges, rects);
  const taken: Rect[] = [...rects.values()];
  const text = (t: string, x: number, y: number, size: number, font: string, color: string, align: CanvasTextAlign = 'left') => {
    ctx.font = font; ctx.fillStyle = color; ctx.textAlign = align; ctx.textBaseline = 'middle';
    ctx.fillText(t, x, y);
    const w = ctx.measureText(t).width;
    labels.push({ text: t, x: align === 'center' ? x - w / 2 : x, y: y - size * 0.575, w, h: size * 1.15, size });
  };
  const fits = (r: Rect) => r[0] >= 0 && r[1] >= 0 && r[2] <= s.w && r[3] <= s.h && free(taken, r);
  ctx.globalAlpha = s.t;

  // Column headers, when they fit.
  if (s.t >= 1) {
    for (const c of L.columns) {
      const [cx] = toScreen(s.view, c.x + FC.SW / 2, 0), [, cy] = toScreen(s.view, 0, c.top);
      const title = columnTitle(c.hop, L.focus.name), sub = columnCount(c);
      const hf = `600 15px ${P.display}`, sf = `400 ${MIN_FONT}px ${P.sans}`;
      ctx.font = hf; const w1 = ctx.measureText(title).width; ctx.font = sf; const w2 = ctx.measureText(sub).width;
      const w = Math.max(w1, w2), r: Rect = [cx - w / 2, cy - 44, cx + w / 2, cy - 6];
      if (!fits(r)) continue;
      taken.push(r);
      text(title, cx, cy - 34, 15, hf, P.text2, 'center');
      text(sub, cx, cy - 16, MIN_FONT, sf, P.text3, 'center');
    }
  }

  // Edges, with an arrowhead in the direction of the call.
  const labs = new Map(L.edges.map((e) => [e.id, edgeLabel(e)]));
  for (const e of L.edges) {
    const g = geo.get(e.id);
    if (!g) continue;
    const lab = labs.get(e.id)!, hot = s.hoverEdge === e.id;
    const col = hot ? P.accent : lab.sev === 2 ? P.err : lab.sev === 1 ? P.warn : e.inferred ? P.neutral : P.text3;
    ctx.globalAlpha = s.t * (hot ? 1 : lab.sev ? 0.95 : 0.8);
    ctx.strokeStyle = col; ctx.fillStyle = col;
    ctx.lineWidth = hot ? 2.6 : lab.sev ? 1.9 : 1.4;
    ctx.setLineDash([...edgeDash(e)]);
    ctx.beginPath(); ctx.moveTo(g[0], g[1]); ctx.bezierCurveTo(g[2], g[3], g[4], g[5], g[6] - 7, g[7]); ctx.stroke();
    ctx.setLineDash([]);
    const ang = Math.atan2(g[7] - g[5], g[6] - g[4]), A = 9;
    ctx.beginPath(); ctx.moveTo(g[6], g[7]);
    ctx.lineTo(g[6] - A * Math.cos(ang - 0.42), g[7] - A * Math.sin(ang - 0.42));
    ctx.lineTo(g[6] - A * Math.cos(ang + 0.42), g[7] - A * Math.sin(ang + 0.42));
    ctx.fill();
  }

  // Edge labels: the hovered and problem labels claim first; one that would overlap is left out.
  if (s.t >= 1) {
    const f = `500 ${MIN_FONT}px ${P.sans}`, gf = `700 ${MIN_FONT}px ${P.sans}`;
    const order = L.edges.filter((e) => geo.has(e.id) && labs.get(e.id)!.text)
      .sort((a, b) => Number(s.hoverEdge === b.id) - Number(s.hoverEdge === a.id) || labs.get(b.id)!.sev - labs.get(a.id)!.sev);
    for (const e of order) {
      const lab = labs.get(e.id)!, g = geo.get(e.id)!, hot = s.hoverEdge === e.id;
      ctx.font = f;
      const tw = ctx.measureText(lab.text).width, w = tw + (lab.sev ? 26 : 12), h = 20;
      let placed: Rect | null = null;
      for (const t of [0.5, 0.38, 0.62, 0.3, 0.7]) {
        const [mx, my] = bez(g, t), r: Rect = [mx - w / 2, my - h / 2, mx + w / 2, my + h / 2];
        if (fits(r)) { placed = r; break; }
      }
      if (!placed) continue;
      taken.push(placed);
      const col = hot ? P.accent : lab.sev === 2 ? P.err : lab.sev === 1 ? P.warn : P.text2;
      ctx.globalAlpha = 1;
      ctx.beginPath(); ctx.roundRect(placed[0], placed[1], w, h, 10); ctx.fillStyle = P.bg; ctx.fill();
      ctx.lineWidth = 1; ctx.strokeStyle = lab.sev || hot ? col : P.border; ctx.stroke();
      let x = placed[0] + 6;
      if (lab.sev) { text('!', x + 4, placed[1] + h / 2, MIN_FONT, gf, col, 'center'); x += 14; }
      text(lab.text, x, placed[1] + h / 2, MIN_FONT, f, col);
    }
  }

  // Cards.
  for (const c of L.cards) drawCard(ctx, c, rects.get(c.node.key)!, s, text);

  // Legend, along the bottom left.
  ctx.globalAlpha = 1;
  const lf = `400 ${MIN_FONT}px ${P.sans}`, ly = s.h - 20;
  ctx.font = lf;
  let lx = 16;
  const lw = FOCUS_LEGEND.reduce((a, it) => a + ctx.measureText(it.label).width + (it.dash ? 30 : 16) + 18, 0);
  if (lw + 24 <= s.w) {
    ctx.fillStyle = P.bg; ctx.globalAlpha = 0.9; ctx.fillRect(8, ly - 13, lw + 8, 26); ctx.globalAlpha = 1;
    for (const it of FOCUS_LEGEND) {
      if (it.dash) {
        ctx.strokeStyle = P.text3; ctx.lineWidth = 1.6; ctx.setLineDash([...it.dash]);
        ctx.beginPath(); ctx.moveTo(lx, ly); ctx.lineTo(lx + 22, ly); ctx.stroke(); ctx.setLineDash([]);
        lx += 30;
      } else {
        text(it.mark!, lx, ly, MIN_FONT, `700 ${MIN_FONT}px ${P.sans}`, it.mark === '!' ? P.err : P.text2);
        lx += 16;
      }
      text(it.label, lx, ly, MIN_FONT, lf, P.text2);
      lx += ctx.measureText(it.label).width + 18;
    }
  }
  ctx.globalAlpha = 1;
  return { labels, rects, geo };
}

type Text = (t: string, x: number, y: number, size: number, font: string, color: string, align?: CanvasTextAlign) => void;

/** clip shortens s with an ellipsis until it fits max pixels. */
function clip(ctx: CanvasRenderingContext2D, s: string, font: string, max: number): string {
  ctx.font = font;
  if (ctx.measureText(s).width <= max) return s;
  let t = s;
  while (t.length > 3 && ctx.measureText(`${t}…`).width > max) t = t.slice(0, -1);
  return `${t}…`;
}

/** drawCard draws one card: the focus large, its name, version and owner, what Pacto knows and its worst attention item. */
function drawCard(ctx: CanvasRenderingContext2D, c: FocusCard, r: Rect, s: FocusScene, text: Text) {
  const P = s.palette, n = c.node, centre = c.col === 0, hov = s.hoverNode === n.key;
  const [x, y] = r, w = r[2] - r[0], h = r[3] - r[1];
  const sev = n.severity === 'error' || n.severity === 'warning' ? n.severity : null;
  ctx.globalAlpha = s.t;
  ctx.beginPath(); ctx.roundRect(x, y, w, h, 7); ctx.fillStyle = centre ? P.raised : P.bg; ctx.fill();
  ctx.lineWidth = centre || hov || sev === 'error' ? 2 : 1;
  ctx.strokeStyle = centre || hov ? P.accent : sev === 'error' ? P.err : sev === 'warning' ? P.warn : P.borderStrong;
  ctx.setLineDash(n.inferred || n.match === 'expected-only' ? [4, 3] : []);
  ctx.stroke(); ctx.setLineDash([]);
  if (!n.inferred && !n.external) {
    ctx.fillStyle = P[statusTone(n.status)] ?? P.neutral;
    ctx.beginPath(); ctx.roundRect(x, y, Math.min(5, w), h, [7, 0, 0, 7]); ctx.fill();
  }
  const owner = n.external ? 'external system' : n.owner || 'no owner';
  const known = n.external ? 'outside Pacto' : n.inferred ? 'inferred, no contract' : KNOWN[n.match ?? ''] ?? 'declared, no evidence';
  const att = sev && n.worst ? `${SEVERITY_GLYPH[sev]} ${attentionCategoryLabel(n.worst)}` : '';
  const attCol = sev === 'error' ? P.err : P.warn;
  const name = n.label || n.name;
  const meta = join(n.version ? `v${n.version}` : '', owner);
  const lines: [string, string, string][] = centre
    ? [[name, `700 15px ${P.sans}`, P.text], [meta, `400 ${MIN_FONT}px ${P.sans}`, P.text2],
      [`${countOf(n.interfaces ?? 0, 'interface')} · ${known}`, `400 ${MIN_FONT}px ${P.sans}`, P.text3],
      [`${n.in ?? 0} call it · it calls ${n.out ?? 0}`, `400 ${MIN_FONT}px ${P.sans}`, P.text3],
      [att, `600 ${MIN_FONT}px ${P.sans}`, attCol]]
    : [[name, `600 ${MIN_FONT}px ${P.sans}`, P.text], [meta, `400 ${MIN_FONT}px ${P.sans}`, P.text2],
      [att || known, `${att ? 600 : 400} ${MIN_FONT}px ${P.sans}`, att ? attCol : P.text3]];
  const shown = lines.filter((l) => l[0]);
  const lh = centre ? 20 : 18, max = w - 22;
  // Omit, never squeeze: only the lines that fit the card at this zoom.
  const room = Math.max(0, Math.floor((h - 6) / lh));
  // A card the canvas edge cuts stays unnamed until it is panned into view.
  const cut = x < 0 || y < 0 || r[2] > s.w || r[3] > s.h;
  if (max < 30 || !room || cut) { ctx.globalAlpha = 1; return; }
  const take = shown.slice(0, room);
  let yy = y + (h - take.length * lh) / 2 + lh / 2;
  for (const [t, f, col] of take) {
    const size = parseFloat(/(\d+)px/.exec(f)![1]);
    text(clip(ctx, t, f, max), x + 14, yy, size, f, col);
    yy += lh;
  }
  ctx.globalAlpha = 1;
}

/** hitFocus is the card under a screen point, else the edge within 7px of it. */
export function hitFocus(L: FocusLayout, f: FocusFrame, sx: number, sy: number): { node?: FocusNode; edge?: FocusEdge } {
  for (const c of L.cards) {
    const r = f.rects.get(c.node.key);
    if (r && sx >= r[0] && sx <= r[2] && sy >= r[1] && sy <= r[3]) return { node: c.node };
  }
  let best: FocusEdge | undefined, bd = 49;
  for (const e of L.edges) {
    const g = f.geo.get(e.id);
    if (!g) continue;
    for (let i = 0; i <= 20; i++) {
      const [bx, by] = bez(g, i / 20), d = (bx - sx) ** 2 + (by - sy) ** 2;
      if (d < bd) { bd = d; best = e; }
    }
  }
  return best ? { edge: best } : {};
}
