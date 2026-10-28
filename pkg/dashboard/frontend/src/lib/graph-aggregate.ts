/** Dependency-graph aggregation: worst status, related subgraphs and the per-owner graph. */

import { formatCount } from './format';

// ── Dependency graph aggregation ──

const STATUS_SEVERITY: Record<string, number> = {
  Invalid: 5, NonCompliant: 4, Unknown: 3, Warning: 2, external: 1, Reference: 0, NotEvaluated: 0, Compliant: 0,
};
/** The worst (most-severe) status in a set — used to color an aggregated node. */
export function worstStatus(statuses: Iterable<string>): string {
  let worst = 'Compliant', sev = -1;
  for (const s of statuses) {
    const v = STATUS_SEVERITY[s] ?? 0;
    if (v > sev) { sev = v; worst = s; }
  }
  return worst;
}

interface GNode { id: string; serviceName: string; status: string; edges?: Array<{ targetId: string; type?: string }> }

/**
 * A subgraph of the "core" nodes (e.g. one team's services) plus their related
 * nodes — always their direct dependencies, and (optionally) their direct
 * dependents. Unrelated services are dropped, so an owner view shows the team and
 * what it touches, not the whole fleet.
 *
 * `includeDependents` defaults to false: when a team owns something everyone
 * depends on (e.g. postgres), pulling in every dependent would drag in the whole
 * fleet and defeat the point of focusing the team. Deps are always bounded by
 * what the team's services actually consume.
 */
export function relatedSubgraph<T extends GNode>(
  graphData: { nodes?: T[] } | null,
  isCore: (node: T) => boolean,
  { includeDependents = false }: { includeDependents?: boolean } = {},
): { nodes: T[] } {
  const nodes = graphData?.nodes || [];
  const core = new Set(nodes.filter(isCore).map((n) => n.id));
  if (!core.size) return { nodes: [] };
  const keep = new Set(core);
  for (const n of nodes) {
    if (core.has(n.id)) for (const e of n.edges || []) keep.add(e.targetId); // deps
    if (includeDependents) for (const e of n.edges || []) if (core.has(e.targetId)) keep.add(n.id);
  }
  return { nodes: nodes.filter((n) => keep.has(n.id)) };
}

interface AggNode { id: string; serviceName: string; status: string; version?: string; edges?: Array<{ targetId: string; type?: string; required?: boolean }> }

/**
 * Collapse a service dependency graph into a per-owner one: each owner becomes a
 * single node (status = its worst service, label = service count), and edges are
 * the DISTINCT cross-owner dependencies. Answers "which team depends on which".
 * `ownerLabelOf` maps a service node to its owner label.
 */
export function aggregateGraphByOwner(
  graphData: { nodes?: Array<{ id: string; serviceName: string; status: string; edges?: Array<{ targetId: string; type?: string }> }> } | null,
  ownerLabelOf: (node: { id: string; serviceName: string; status: string }) => string,
): { nodes: AggNode[] } {
  const nodes = graphData?.nodes || [];
  const ownerOf = new Map<string, string>();
  for (const n of nodes) ownerOf.set(n.id, ownerLabelOf(n));

  const stats = new Map<string, { count: number; statuses: string[] }>();
  for (const n of nodes) {
    const o = ownerOf.get(n.id);
    if (!o) continue;
    const a = stats.get(o) ?? stats.set(o, { count: 0, statuses: [] }).get(o)!;
    a.count++; a.statuses.push(n.status);
  }
  const targets = new Map<string, Set<string>>();
  for (const o of stats.keys()) targets.set(o, new Set());
  for (const n of nodes) {
    const so = ownerOf.get(n.id);
    for (const e of n.edges || []) {
      const to = ownerOf.get(e.targetId);
      if (so && to && so !== to) targets.get(so)!.add(to);
    }
  }
  return {
    nodes: [...stats.entries()].map(([owner, a]) => ({
      id: owner,
      serviceName: owner,
      status: worstStatus(a.statuses),
      version: `${formatCount(a.count)} service${a.count !== 1 ? 's' : ''}`,
      edges: [...(targets.get(owner) || [])].map((t) => ({ targetId: t, type: 'dependency', required: true })),
    })),
  };
}
