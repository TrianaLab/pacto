/**
 * Pure layout helper for the focused-neighborhood subgraph used by GraphCanvas
 * on large fleets. No DOM — data in, data out — so it is fully unit-testable.
 * (Node positioning + edge routing are handled by Cytoscape/dagre in graph.ts.)
 */
import type { GraphNode, GraphData } from './graph.ts';

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
