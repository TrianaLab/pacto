/**
 * Pure adapter from a bounded product ProductNeighborhood to the GraphData the shared
 * Cytoscape engine (lib/graph.ts) renders. It invents no nodes or edges: it is a
 * structural projection of the backend answer, so the visual topology shows EXACTLY
 * what the backend returned (every returned node, every returned edge between returned
 * nodes) and never re-infers relationships in the frontend.
 */
import type { GraphData, GraphNode, GraphEdge } from './graph.ts';
import type { ProductNeighborhood } from './api.ts';

// The adapter's INPUT is DERIVED from the generated ProductNeighborhood (the single wire
// truth), never a hand-mirrored partial: it reads only a neighborhood's nodes + edges, so
// its input is the Pick of those two, with the node/edge element types coming straight from
// the generated SDK via indexed access. An OpenAPI change flows in automatically and a
// re-introduced hand mirror cannot silently drift (guarded in api.typetest.ts). The renderer-owned GraphData/GraphNode/GraphEdge (in graph.ts) stay an
// explicit internal presentation model -- they are NOT wire DTOs.
type NeighborhoodLike = Partial<Pick<ProductNeighborhood, 'nodes' | 'edges'>>;

// EdgeState is the restrained visual vocabulary the canvas renders (see cyStylesheet):
// one state per legend item, so every legend item is a real visual distinction. It is
// DERIVED from the backend-authoritative difference (service projection) or
// serviceCorroboration (fine-grained projection), never re-inferred from booleans.
export type EdgeVisualState = '' | 'matched' | 'expected-not-observed' | 'drift' | 'insufficient';

/** edgeState collapses the backend edge verdict into the canvas visual vocabulary. A
 *  service-projection edge carries `difference`; a fine-grained (revision/target) edge
 *  carries `serviceCorroboration`. Both map to the SAME visual states (the drawer keeps
 *  the precise wording). An edge with neither (an expected-only view) has no state and
 *  renders as a plain declared edge. */
export function edgeState(e: { difference?: string; serviceCorroboration?: string }): EdgeVisualState {
  switch (e.difference) {
    case 'matched': return 'matched';
    case 'observed-not-expected': return 'drift';
    case 'expected-not-observed': return 'expected-not-observed';
    case 'insufficient': return 'insufficient';
  }
  switch (e.serviceCorroboration) {
    case 'matched': return 'matched';
    case 'expected-not-observed': return 'expected-not-observed';
    case 'insufficient': return 'insufficient';
  }
  return '';
}

/** cyEdgeId reproduces the Cytoscape edge id that buildElements assigns, so a canvas
 *  edge tap (which reports the Cytoscape id) can be matched back to its backend edge.
 *  It MUST stay in sync with buildElements' `${source}->${target}:${etype}` id. */
export function cyEdgeId(fromKey: string, toKey: string, relation: string): string {
  return `${fromKey}→${toKey}:${edgeType(relation)}`;
}

/** edgeType maps a backend relation to the Cytoscape edge `etype`. "runs" keeps its own
 *  type so the stylesheet renders it dashed/distinct; everything else is a dependency. */
function edgeType(relation: string): string {
  return relation === 'runs' ? 'runs' : 'dependency';
}

/** nodeKind narrows a backend entity kind to the three kinds the graph renders. */
function nodeKind(kind: string): 'service' | 'revision' | 'target' {
  return kind === 'revision' || kind === 'target' ? kind : 'service';
}

/** neighborhoodToGraph adapts a ProductNeighborhood into GraphData: one GraphNode per
 *  returned node (keyed by its canonical (kind,key) so the id is unique across mixed
 *  kinds), each edge attached to its source node, and only edges whose BOTH endpoints
 *  are returned nodes (the backend already bounds the set). */
export function neighborhoodToGraph(nb: NeighborhoodLike | null | undefined): GraphData {
  const nodes: GraphNode[] = [];
  const byKey = new Map<string, GraphNode>();
  for (const n of nb?.nodes ?? []) {
    // A revision reads "service version"; the version goes on its own label line so
    // truncating a long service name never cuts the version off.
    const version = n.ref.kind === 'revision' ? n.ref.version || '' : '';
    const label = n.ref.label || n.ref.key;
    const g: GraphNode = {
      id: n.ref.key,
      serviceName: version && label.endsWith(` ${version}`) ? label.slice(0, -version.length - 1) : label,
      ...(version ? { version } : {}),
      status: n.status || n.ref.status || 'Unknown',
      kind: nodeKind(n.ref.kind),
      edges: [],
      ...(typeof n.depth === 'number' ? { depth: n.focus ? 0 : n.depth } : n.focus ? { depth: 0 } : {}),
      ...(n.owner ? { owner: n.owner } : {}),
    };
    nodes.push(g);
    byKey.set(g.id, g);
  }
  for (const e of nb?.edges ?? []) {
    const src = byKey.get(e.from.key);
    if (!src || !byKey.has(e.to.key)) continue;
    // Carry the backend reconciliation state (difference for a service edge, service
    // corroboration for a fine-grained one) into a single visual state the canvas
    // renders as a real distinction. The drawer keeps the precise, scoped wording.
    const state = edgeState(e);
    const edge: GraphEdge = {
      targetId: e.to.key,
      type: edgeType(e.relation),
      edgeState: state || undefined,
      driftStatus: state === 'drift' ? 'drift' : undefined,
    };
    src.edges!.push(edge);
  }
  return { nodes };
}

/** topoSignature captures ONLY a neighborhood graph's shape: node ids and each edge's
 *  endpoints and type. Two graphs with the same topology signature can be updated in
 *  place (no relayout); a change requires a rebuild. Deterministic (sorted). */
export function topoSignature(gd: GraphData): string {
  const ns = gd.nodes.map((n) => n.id).sort().join(',');
  const es = gd.nodes
    .flatMap((n) => (n.edges || []).map((e) => `${n.id}>${e.targetId}:${e.type || 'dependency'}`))
    .sort().join(',');
  return `${ns}||${es}`;
}

/** presentationSignature captures the MUTABLE visual fields that Cytoscape styles read
 *  (node status/label/kind, edge state) but that do NOT change the topology. When it
 *  changes with an unchanged topoSignature, the canvas is patched in place so a
 *  semantic-only refresh (e.g. Compliant -> NonCompliant, or a difference verdict) is
 *  never left stale. Deterministic (sorted). */
export function presentationSignature(gd: GraphData): string {
  const ns = gd.nodes
    .map((n) => `${n.id}:${n.status}:${n.kind || 'service'}:${n.serviceName}`)
    .sort().join(',');
  const es = gd.nodes
    .flatMap((n) => (n.edges || []).map((e) => `${n.id}>${e.targetId}:${e.type || 'dependency'}:${e.edgeState || ''}`))
    .sort().join(',');
  return `${ns}||${es}`;
}

/** Past this many entities a neighborhood folds the ones beyond the direct neighbors
 *  into one node per team: 200 boxes on one canvas read as a mass, not a graph. */
export const GROUP_THRESHOLD = 40;

/** The id prefix of a node that stands for one team's entities. */
export const GROUP_PREFIX = 'group:';

export interface Grouping {
  /** Owners whose group the reader opened. */
  open?: ReadonlySet<string>;
  /** Show every entity, grouping nothing. */
  all?: boolean;
  threshold?: number;
}

export interface GroupedGraph {
  graph: GraphData;
  /** The groups drawn, largest first, and how many entities each stands for. */
  groups: Array<{ owner: string; count: number }>;
  /** How many entities the groups fold away. */
  folded: number;
}

/** The owner a group is keyed by; an entity with none groups as "No owner". */
const ownerOf = (n: GraphNode) => n.owner || 'No owner';

/**
 * groupByOwner folds a large neighborhood so it stays readable: past the threshold,
 * the entities beyond the direct neighbors (and the direct neighbors too, when they
 * alone are past it) become one node per owner, with an edge wherever one of its
 * entities had one. The focus is never folded, a team of one is never folded and an
 * opened team shows its entities. It hides nothing from the text alternative: the
 * Relationships list still names every entity.
 */
export function groupByOwner(gd: GraphData, { open = new Set(), all = false, threshold = GROUP_THRESHOLD }: Grouping = {}): GroupedGraph {
  if (all || gd.nodes.length <= threshold) return { graph: gd, groups: [], folded: 0 };
  const far = (n: GraphNode) => (n.depth ?? 1) >= 2;
  const near = (n: GraphNode) => (n.depth ?? 1) === 1;
  // Fold the far ring; if the near ring alone is still too many, fold it as well.
  const foldNear = gd.nodes.filter((n) => !far(n)).length > threshold;
  const candidates = gd.nodes.filter((n) => (far(n) || (foldNear && near(n))) && !open.has(ownerOf(n)));
  const members = new Map<string, GraphNode[]>();
  for (const n of candidates) members.set(ownerOf(n), [...(members.get(ownerOf(n)) ?? []), n]);
  const rep = new Map<string, string>(); // entity id -> the id that stands for it
  const groups: GroupedGraph['groups'] = [];
  const nodes: GraphNode[] = [];
  for (const [owner, ms] of members) {
    if (ms.length < 2) continue;
    const id = GROUP_PREFIX + owner;
    for (const m of ms) rep.set(m.id, id);
    groups.push({ owner, count: ms.length });
    nodes.push({ id, serviceName: owner, version: `${ms.length} entities`, status: 'group', kind: 'service', owner,
      depth: Math.min(...ms.map((m) => m.depth ?? 1)), group: ms.length, edges: [] });
  }
  if (!groups.length) return { graph: gd, groups: [], folded: 0 };
  const byId = new Map<string, GraphNode>();
  for (const n of gd.nodes) {
    if (rep.has(n.id)) continue;
    const kept = { ...n, edges: [] as GraphEdge[] };
    nodes.push(kept);
    byId.set(n.id, kept);
  }
  for (const g of nodes) if (g.group) byId.set(g.id, g);
  const seen = new Set<string>();
  for (const n of gd.nodes) {
    for (const e of n.edges ?? []) {
      const from = rep.get(n.id) ?? n.id;
      const to = rep.get(e.targetId) ?? e.targetId;
      if (from === to || !byId.has(to)) continue; // inside one team, or not drawn
      const key = `${from}>${to}:${e.type || 'dependency'}`;
      if (seen.has(key)) continue;
      seen.add(key);
      // One edge for many: it keeps a state only when it is an entity's own edge.
      const folded = from !== n.id || to !== e.targetId;
      byId.get(from)!.edges!.push(folded ? { targetId: to, type: e.type } : { ...e });
    }
  }
  groups.sort((a, b) => b.count - a.count || a.owner.localeCompare(b.owner));
  return { graph: { nodes }, groups, folded: candidates.filter((n) => rep.has(n.id)).length };
}
