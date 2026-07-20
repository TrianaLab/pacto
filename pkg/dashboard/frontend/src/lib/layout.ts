/**
 * Pure layout helpers for the dependency-tree view. No DOM — data in, data out —
 * so they are fully unit-testable. (Cytoscape/dagre computes the initial layered
 * positions; wrapWideRanks post-processes them.)
 */
import type { GraphNode, GraphData } from './graph.ts';

/**
 * Fold any rank wider than `maxWidth` into stacked sub-rows. In a layered
 * dependency tree one level (e.g. ~15 mid-tier services) is often far wider than
 * the others (2-3 roots, a few shared-infra sinks); left as a single row it makes
 * the whole tree an illegible band. Wrapping that level into a compact block of
 * rows keeps every level balanced and the tree readable in one view. Within-rank
 * order (dagre's crossing-minimised x) is preserved, each row is re-centred, and
 * lower ranks are pushed down by the height the wrap added. Mutates `pos`.
 */
export function wrapWideRanks(
  pos: Map<string, { x: number; y: number }>,
  { nodeW, nodeH, nodesep, ranksep, maxWidth }:
    { nodeW: number; nodeH: number; nodesep: number; ranksep: number; maxWidth: number },
): void {
  const entries = [...pos.entries()];
  if (!entries.length) return;
  const byRank = new Map<number, string[]>();
  for (const [id, p] of entries) {
    const key = Math.round(p.y);
    (byRank.get(key) ?? byRank.set(key, []).get(key)!).push(id);
  }
  const rankKeys = [...byRank.keys()].sort((a, b) => a - b);
  const maxCols = Math.max(1, Math.floor(maxWidth / (nodeW + nodesep)));
  if (![...byRank.values()].some((r) => r.length > maxCols)) return;

  const xs = entries.map(([, p]) => p.x);
  const cx = (Math.min(...xs) + Math.max(...xs)) / 2;
  const step = nodeW + nodesep;
  // Sub-rows of the same rank sit closer together than a full rank gap so they
  // read as one tier, not as separate dependency levels.
  const subRowH = nodeH + ranksep * 0.45;
  let top = rankKeys[0];
  for (const key of rankKeys) {
    const ids = byRank.get(key)!.sort((a, b) => pos.get(a)!.x - pos.get(b)!.x);
    const rows = Math.ceil(ids.length / maxCols);
    // Balance the rows (e.g. 15 over 3 rows → 5/5/5, not 6/6/3).
    const perRow = Math.ceil(ids.length / rows);
    for (let r = 0; r < rows; r++) {
      const rowIds = ids.slice(r * perRow, (r + 1) * perRow);
      const startX = cx - ((rowIds.length - 1) * step) / 2;
      rowIds.forEach((id, i) => pos.set(id, { x: startX + i * step, y: top + r * subRowH }));
    }
    top += (rows - 1) * subRowH + nodeH + ranksep;
  }
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
