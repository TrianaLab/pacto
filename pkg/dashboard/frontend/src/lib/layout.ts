/**
 * Pure layout helpers for the layered (hierarchical) graph mode.
 * No DOM, no d3 — data in, data out — so it is fully unit-testable.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
import dagre from '@dagrejs/dagre';
import type { GraphNode, GraphData } from './graph.ts';

export interface LayeredOptions {
  direction?: 'TB' | 'LR';
  nodeW: number;
  nodeH: number;
  nodesep?: number;
  ranksep?: number;
  /** When set (TB only), fold any rank wider than this into stacked sub-rows so a
   *  wide, shallow fleet fits at a readable scale instead of a pan-only band. */
  wrapWidth?: number;
}

/**
 * Fold ranks wider than `maxWidth` into centered sub-rows. A tiered fleet (2 roots
 * → many mid-tier services → few sinks) is otherwise far wider than tall, so it can
 * only be read by panning; wrapping the fat rank into rows makes the whole shape
 * fit. Within-rank order (dagre's crossing-minimized x) is preserved so connected
 * nodes stay near each other; every rank is re-centred on a common axis and lower
 * ranks are pushed down by the height the wrap added. Mutates `pos`.
 */
export function wrapWideRanks(
  pos: Map<string, { x: number; y: number }>,
  { nodeW, nodeH, nodesep, ranksep, maxWidth }:
    { nodeW: number; nodeH: number; nodesep: number; ranksep: number; maxWidth: number },
): void {
  const entries = [...pos.entries()];
  if (!entries.length) return;
  // Group into ranks by y (dagre gives every node in a rank the same y).
  const byRank = new Map<number, string[]>();
  for (const [id, p] of entries) {
    const key = Math.round(p.y);
    (byRank.get(key) ?? byRank.set(key, []).get(key)!).push(id);
  }
  const rankKeys = [...byRank.keys()].sort((a, b) => a - b);
  const maxCols = Math.max(1, Math.floor(maxWidth / (nodeW + nodesep)));
  // Nothing overflows → leave dagre's layout untouched.
  if (![...byRank.values()].some((r) => r.length > maxCols)) return;

  const xs = entries.map(([, p]) => p.x);
  const cx = (Math.min(...xs) + Math.max(...xs)) / 2;
  const step = nodeW + nodesep;
  // Sub-rows of the SAME rank sit closer together (tighter than a full ranksep) so
  // they read as one tier, not as separate dependency ranks; distinct tiers keep
  // the full ranksep gap. `top` tracks the centre-y of the current rank's first row.
  const subRowH = nodeH + ranksep * 0.4;
  let top = rankKeys[0];
  for (const key of rankKeys) {
    const ids = byRank.get(key)!.sort((a, b) => pos.get(a)!.x - pos.get(b)!.x);
    const rows = Math.ceil(ids.length / maxCols);
    for (let r = 0; r < rows; r++) {
      const rowIds = ids.slice(r * maxCols, (r + 1) * maxCols);
      const startX = cx - ((rowIds.length - 1) * step) / 2;
      rowIds.forEach((id, i) => pos.set(id, { x: startX + i * step, y: top + r * subRowH }));
    }
    top += (rows - 1) * subRowH + nodeH + ranksep;
  }
}

/** Compute node center positions with a Sugiyama layered layout (dagre). */
export function layeredPositions(
  nodes: GraphNode[],
  links: Array<{ source: string; target: string }>,
  { direction = 'TB', nodeW, nodeH, nodesep = 40, ranksep = 70, wrapWidth }: LayeredOptions,
): Map<string, { x: number; y: number }> {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const g = new (dagre as any).graphlib.Graph();
  g.setGraph({ rankdir: direction, nodesep, ranksep, marginx: 20, marginy: 20 });
  g.setDefaultEdgeLabel(() => ({}));
  const ids = new Set(nodes.map((nn) => nn.id));
  for (const nn of nodes) g.setNode(nn.id, { width: nodeW, height: nodeH });
  for (const l of links) if (ids.has(l.source) && ids.has(l.target)) g.setEdge(l.source, l.target);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (dagre as any).layout(g);
  const pos = new Map<string, { x: number; y: number }>();
  for (const nn of nodes) {
    const gn = g.node(nn.id);
    if (gn) pos.set(nn.id, { x: gn.x, y: gn.y });
  }
  if (wrapWidth && direction === 'TB') {
    wrapWideRanks(pos, { nodeW, nodeH, nodesep, ranksep, maxWidth: wrapWidth });
  }
  return pos;
}

/**
 * Legibility floor for the initial graph zoom. A wide, shallow DAG (few roots →
 * many mid-tier services → few shared sinks) lays out far wider than tall; a
 * plain fit-to-content then shrinks every node to an illegible band. Flooring the
 * scale keeps labels readable (~13px * 0.7 ≈ 9px) and lets the graph pan instead.
 * ponytail: single tunable knob — raise for crisper labels + more panning.
 */
export const LEGIBLE_SCALE_FLOOR = 0.7;

/**
 * Compute a zoom transform that fits `nodes` into `canvas`, clamped between a
 * legibility floor and a max. Single source of truth for the two initial-fit
 * sites in the renderer (layered fit + force focus auto-center), so every
 * dependency graph — fleet, service detail, owner — fits the same way.
 */
export function fitTransform(
  nodes: Array<{ x?: number; y?: number }>,
  canvas: { width: number; height: number },
  { nodeW, nodeH, pad = 40, floor = LEGIBLE_SCALE_FLOOR, max = 1.5 }:
    { nodeW: number; nodeH: number; pad?: number; floor?: number; max?: number },
): { scale: number; tx: number; ty: number } {
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  for (const nn of nodes) {
    const x = nn.x ?? 0, y = nn.y ?? 0;
    if (x - nodeW / 2 < minX) minX = x - nodeW / 2;
    if (x + nodeW / 2 > maxX) maxX = x + nodeW / 2;
    if (y - nodeH / 2 < minY) minY = y - nodeH / 2;
    if (y + nodeH / 2 > maxY) maxY = y + nodeH / 2;
  }
  if (!Number.isFinite(minX)) return { scale: 1, tx: 0, ty: 0 };
  minX -= pad; minY -= pad; maxX += pad; maxY += pad;
  const bw = maxX - minX || 1, bh = maxY - minY || 1;
  const scale = Math.min(Math.max(Math.min(canvas.width / bw, canvas.height / bh), floor), max);
  const cx = (minX + maxX) / 2, cy = (minY + maxY) / 2;
  return { scale, tx: canvas.width / 2 - cx * scale, ty: canvas.height / 2 - cy * scale };
}

export interface VisibleOptions {
  rootId: string;
  direction: 'down' | 'up';
  depth: number;
  expanded: Set<string>;
  childCap: number;
}
export interface VisibleResult {
  nodes: GraphNode[];
  hidden: Map<string, number>;
}

/** Depth/expand/child-cap-limited subgraph rooted at rootId, plus per-node hidden-child counts. */
export function computeVisible(graphData: GraphData, opts: VisibleOptions): VisibleResult {
  const { rootId, direction, depth, expanded, childCap } = opts;
  const all = graphData?.nodes || [];
  const nodeMap = new Map(all.map((nn) => [nn.id, nn]));
  const root = all.find((nn) => nn.id === rootId || nn.serviceName === rootId);
  if (!root) return { nodes: [], hidden: new Map() };

  // direction-children adjacency:
  //  'down' -> a node's own edge targets (its dependencies/references)
  //  'up'   -> nodes that point at it (its dependents)
  // Deduped by targetId (a node can have both a dependency and a config/policy
  // reference to the same target) so the cap and "+N" hidden count agree.
  const childSets = new Map<string, Set<string>>();
  for (const nn of all) childSets.set(nn.id, new Set());
  for (const nn of all) {
    for (const e of nn.edges || []) {
      if (!nodeMap.has(e.targetId)) continue;
      if (direction === 'down') childSets.get(nn.id)!.add(e.targetId);
      else childSets.get(e.targetId)!.add(nn.id);
    }
  }
  const childrenOf = new Map<string, string[]>();
  for (const [k, v] of childSets) childrenOf.set(k, [...v]);

  const visible = new Set<string>([root.id]);
  const depthOf = new Map<string, number>([[root.id, 0]]);
  const queue: string[] = [root.id];
  while (queue.length) {
    const id = queue.shift()!;
    const d = depthOf.get(id)!;
    if (!(expanded.has(id) || d < depth)) continue; // no descent
    const cap = expanded.has(id) ? Infinity : childCap;
    let shown = 0;
    for (const k of childrenOf.get(id) || []) {
      if (visible.has(k)) continue;
      if (shown >= cap) break;
      visible.add(k);
      depthOf.set(k, d + 1);
      queue.push(k);
      shown++;
    }
  }

  const hidden = new Map<string, number>();
  for (const id of visible) {
    let miss = 0;
    for (const k of childrenOf.get(id) || []) if (!visible.has(k)) miss++;
    if (miss > 0) hidden.set(id, miss);
  }
  return { nodes: all.filter((nn) => visible.has(nn.id)), hidden };
}
