/**
 * Pure adapter from a bounded product ProductNeighborhood to the GraphData the shared
 * Cytoscape engine (lib/graph.ts) renders. It invents no nodes or edges: it is a
 * structural projection of the backend answer, so the visual topology shows EXACTLY
 * what the backend returned (every returned node, every returned edge between returned
 * nodes) and never re-infers relationships in the frontend.
 */
import type { GraphData, GraphNode, GraphEdge } from './graph.ts';
import type { ProductNeighborhood } from './api.ts';
import { statusTone } from './format.ts';

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
 *  (node status/label/kind, edge state and words) but that do NOT change the topology. When it
 *  changes with an unchanged topoSignature, the canvas is patched in place so a
 *  semantic-only refresh (e.g. Compliant -> NonCompliant, or a difference verdict) is
 *  never left stale. Deterministic (sorted). */
export function presentationSignature(gd: GraphData): string {
  const ns = gd.nodes
    .map((n) => `${n.id}:${n.status}:${n.kind || 'service'}:${n.serviceName}:${n.version || ''}`)
    .sort().join(',');
  const es = gd.nodes
    .flatMap((n) => (n.edges || []).map((e) => `${n.id}>${e.targetId}:${e.type || 'dependency'}:${e.edgeState || ''}:${e.label || ''}`))
    .sort().join(',');
  return `${ns}||${es}`;
}

/** Past this many entities a neighborhood folds the ones beyond the direct neighbors
 *  into one node per team: 200 boxes on one canvas read as a mass, not a graph. */
export const GROUP_THRESHOLD = 40;

/** The id prefix of a node that stands for one team's entities. */
export const GROUP_PREFIX = 'group:';

/** The id prefix of a lane: the box holding the direct neighbors on one side. */
export const LANE_PREFIX = 'lane:';

/** At most this many teams per side get a group of their own; the rest, and every team
 *  with a single entity out there, share one "Other teams" group. */
export const MAX_GROUPS_PER_SIDE = 7;

/** A lane shows at most this many direct neighbors; the rest wait behind one "+N more"
 *  node, so a lane never wraps into a second column with lines running under its boxes. */
export const LANE_CAP = 8;

/** The id suffix of a lane's "+N more" node (after GROUP_PREFIX and the side). */
export const MORE_SUFFIX = ':+';

/** The name of the group the small teams share. */
export const OTHER_TEAMS = 'Other teams';

/** Which side of the focus an entity sits on: what the focus depends on (left) or what
 *  uses the focus (right). */
export type Side = 'depends-on' | 'used-by';

export interface Grouping {
  /** Ids of the groups the reader opened. */
  open?: ReadonlySet<string>;
  /** Show every entity, grouping nothing. */
  all?: boolean;
  threshold?: number;
}

export interface LaneGroup { id: string; owner: string; side: Side; count: number }

export interface GroupedGraph {
  graph: GraphData;
  /** The groups drawn, largest first, and how many entities each stands for. */
  groups: LaneGroup[];
  /** How many entities the groups fold away. */
  folded: number;
  /** The lanes' "+N more" nodes, and how many direct neighbors each holds back. */
  more?: LaneGroup[];
}

/** The owner a group is keyed by; an entity with none groups as "No owner". */
const ownerOf = (n: GraphNode) => n.owner || 'No owner';

// The worst state wins on an edge that stands for many: one unexpected call in a team of
// twelve is the thing to see. The words are the legend's.
const STATE_RANK: Record<string, number> = { drift: 4, 'expected-not-observed': 3, insufficient: 2, matched: 1 };
const TONE_RANK: Record<string, number> = { err: 2, warn: 1 };
const STATE_WORDS: Record<string, string> = {
  drift: 'observed, not expected', 'expected-not-observed': 'expected, not observed', insufficient: 'insufficient evidence', matched: 'matched',
};

/**
 * sidesOf puts every entity on one side of the focus. A direct neighbor the focus points
 * at is something it depends on; one that points at the focus uses it (both ways: depends
 * on wins). Everything further out takes the side of the direct neighbor it is reached
 * through, nearest first, and anything not reached at all sits on the used-by side.
 */
export function sidesOf(gd: GraphData, focusId: string): Map<string, Side> {
  const adj = new Map<string, string[]>();
  const link = (a: string, b: string) => { const l = adj.get(a); if (l) l.push(b); else adj.set(a, [b]); };
  const out = new Map<string, Side>();
  for (const n of gd.nodes) {
    for (const e of n.edges ?? []) {
      link(n.id, e.targetId);
      link(e.targetId, n.id);
      if (n.id === focusId && e.targetId !== focusId) out.set(e.targetId, 'depends-on');
    }
  }
  for (const n of gd.nodes) if (n.id !== focusId && !out.has(n.id) && (n.edges ?? []).some((e) => e.targetId === focusId)) out.set(n.id, 'used-by');
  const queue = [...out.keys()];
  for (const id of queue) { // the array grows as it is walked: a breadth-first search
    for (const m of adj.get(id) ?? []) {
      if (m !== focusId && !out.has(m)) { out.set(m, out.get(id)!); queue.push(m); }
    }
  }
  for (const n of gd.nodes) if (n.id !== focusId && !out.has(n.id)) out.set(n.id, 'used-by');
  return out;
}

/**
 * laneGraph lays a focused neighborhood out as lanes: what the focus depends on in a lane
 * on its left, what uses it in a lane on its right. Past the threshold, everything two or
 * more hops away folds into one node per team and side, with ONE edge to its lane carrying
 * how many relationships it stands for and the worst reconciliation state among them, and
 * the direct neighbors fold by team as well when they alone are past it. The focus is
 * never folded, a direct neighbor alone in its team is never folded and an opened group
 * shows its entities. It hides nothing from the text alternative: the Relationships list
 * still names every entity and every relationship. A graph with no focus is returned as
 * it is.
 */
export function laneGraph(gd: GraphData, { open = new Set(), all = false, threshold = GROUP_THRESHOLD }: Grouping = {}): GroupedGraph {
  const focus = gd.nodes.find((n) => n.depth === 0);
  if (!focus) return { graph: gd, groups: [], folded: 0 };
  const side = sidesOf(gd, focus.id);
  const sign = (s: Side | undefined) => (s === 'depends-on' ? -1 : 1);
  const far = (n: GraphNode) => (n.depth ?? 1) >= 2;
  const fold = !all && gd.nodes.length > threshold;
  const foldNear = fold && gd.nodes.filter((n) => n !== focus && !far(n)).length > threshold;

  const members = new Map<string, GraphNode[]>();
  for (const n of gd.nodes) {
    if (n === focus || !fold || (!far(n) && !foldNear)) continue;
    const id = `${GROUP_PREFIX}${side.get(n.id)}:${ownerOf(n)}`;
    if (open.has(id)) continue;
    const ms = members.get(id);
    if (ms) ms.push(n); else members.set(id, [n]);
  }
  // Teams beyond the per-side budget, and teams of one, share an "Other teams" group.
  const ownerName = new Map<string, string>();
  const perSide = new Map<Side, number>();
  const bySize = [...members.entries()].filter(([, ms]) => ms.every(far)).sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]));
  for (const [id, ms] of bySize) {
    const s = side.get(ms[0].id)!;
    const n = perSide.get(s) ?? 0;
    if (ms.length >= 2 && n < MAX_GROUPS_PER_SIDE) { perSide.set(s, n + 1); continue; }
    members.delete(id);
    const other = `${GROUP_PREFIX}${s}:*`; // "*" is no owner's name, so no team collides with it
    if (open.has(other)) continue;
    ownerName.set(other, OTHER_TEAMS);
    members.set(other, [...(members.get(other) ?? []), ...ms]);
  }
  const rep = new Map<string, string>(); // entity id -> the group that stands for it
  const groups: LaneGroup[] = [];
  const groupNodes: GraphNode[] = [];
  for (const [id, ms] of members) {
    if (ms.length < 2 && !far(ms[0])) continue; // a direct neighbor alone in its team stays itself
    for (const m of ms) rep.set(m.id, id);
    const s = side.get(ms[0].id)!;
    const depth = Math.min(...ms.map((m) => m.depth ?? 1));
    const owner = ownerName.get(id) ?? ownerOf(ms[0]);
    groups.push({ id, owner, side: s, count: ms.length });
    groupNodes.push({
      id, serviceName: owner, version: `${ms.length} ${ms.length === 1 ? 'entity' : 'entities'}`, status: 'group', kind: 'service',
      owner, depth, group: ms.length, side: sign(s), edges: [], ...(depth <= 1 ? { parent: LANE_PREFIX + s } : {}),
    });
  }

  // A lane past its cap shows the direct neighbors most worth a look (the worst state on
  // their link with the focus, then the worst status) and holds the rest behind "+N more".
  const attention = (n: GraphNode) => Math.max(0, ...[...(focus.edges ?? []).filter((e) => e.targetId === n.id), ...(n.edges ?? []).filter((e) => e.targetId === focus.id)]
    .map((e) => STATE_RANK[e.edgeState || ''] ?? 0)) * 10 + (TONE_RANK[statusTone(n.status)] ?? 0);
  const more: LaneGroup[] = [];
  for (const s of ['depends-on', 'used-by'] as Side[]) {
    const id = `${GROUP_PREFIX}${s}${MORE_SUFFIX}`;
    if (open.has(id)) continue;
    const near = gd.nodes.filter((n) => n !== focus && !far(n) && !rep.has(n.id) && side.get(n.id) === s);
    // ponytail: a lane of more than LANE_CAP team groups still wraps; folding groups into groups is not worth it yet.
    const room = Math.max(0, LANE_CAP - groupNodes.filter((g) => g.parent === LANE_PREFIX + s).length);
    if (near.length - room < 2) continue; // "+1 more" takes the room the one would
    const hidden = near.sort((a, b) => attention(b) - attention(a) || a.id.localeCompare(b.id)).slice(room);
    for (const m of hidden) rep.set(m.id, id);
    more.push({ id, owner: `+${hidden.length} more`, side: s, count: hidden.length });
    groupNodes.push({
      id, serviceName: `+${hidden.length} more`, version: 'Select to show', status: 'group', kind: 'service',
      owner: '', depth: 1, group: hidden.length, side: sign(s), edges: [], parent: LANE_PREFIX + s,
    });
  }

  // A lane exists for each side anything sits on; its box holds that side's direct neighbors.
  const laneCount = new Map<Side, number>();
  for (const n of gd.nodes) if (n !== focus && !far(n)) laneCount.set(side.get(n.id)!, (laneCount.get(side.get(n.id)!) ?? 0) + 1);
  const used = new Set(side.values());
  const lanes: GraphNode[] = (['depends-on', 'used-by'] as Side[]).filter((s) => used.has(s)).map((s) => ({
    id: LANE_PREFIX + s, serviceName: `${s === 'depends-on' ? 'Depends on' : 'Used by'} (${laneCount.get(s) ?? 0})`,
    status: 'lane', kind: 'service', lane: true, side: sign(s), edges: [],
  }));

  const kept: GraphNode[] = [];
  const byId = new Map<string, GraphNode>();
  for (const n of gd.nodes) {
    if (rep.has(n.id)) continue;
    const s = side.get(n.id);
    const k: GraphNode = n === focus
      ? { ...n, side: 0, edges: [] }
      : { ...n, side: sign(s), edges: [], ...(!far(n) ? { parent: LANE_PREFIX + s } : {}) };
    kept.push(k);
    byId.set(k.id, k);
  }

  // Edges between two drawn entities stay exactly as they are; an edge touching a group
  // is counted into that group's one edge.
  const agg = new Map<string, { count: number; worst: string; worstCount: number; runs: boolean }>();
  const tally = (g: string, e: GraphEdge) => {
    const a = agg.get(g) ?? { count: 0, worst: '', worstCount: 0, runs: true };
    const st = e.edgeState || '';
    a.count++;
    a.runs &&= e.type === 'runs';
    if ((STATE_RANK[st] ?? 0) > (STATE_RANK[a.worst] ?? 0)) { a.worst = st; a.worstCount = 1; } else if (st && st === a.worst) a.worstCount++;
    agg.set(g, a);
  };
  for (const n of gd.nodes) {
    for (const e of n.edges ?? []) {
      const from = rep.get(n.id);
      const to = rep.get(e.targetId);
      if (!from && !to) { if (byId.has(e.targetId)) byId.get(n.id)!.edges!.push({ ...e }); continue; }
      if (from === to) continue; // inside one group
      if (from) tally(from, e);
      if (to) tally(to, e);
    }
  }
  for (const g of groupNodes) {
    const a = agg.get(g.id);
    if (!a) continue;
    const inner = (g.depth ?? 1) <= 1 ? focus.id : LANE_PREFIX + (g.side === -1 ? 'depends-on' : 'used-by');
    const edge: GraphEdge = {
      targetId: g.side === -1 ? g.id : inner,
      type: a.runs ? 'runs' : 'dependency',
      ...(a.worst ? { edgeState: a.worst } : {}),
      // A "+N more" edge sits in the narrow gap by the focus: no room for words there.
      ...(g.id.endsWith(MORE_SUFFIX) ? {} : { label: a.worst ? `${STATE_WORDS[a.worst]} (${a.worstCount})` : `${a.count} ${a.count === 1 ? 'link' : 'links'}` }),
    };
    // Depends-on: the lane points out at the group. Used-by: the group points in.
    if (g.side === -1) (inner === focus.id ? byId.get(focus.id)! : lanes.find((l) => l.id === inner)!).edges!.push(edge);
    else g.edges!.push(edge);
  }
  groups.sort((a, b) => b.count - a.count || a.owner.localeCompare(b.owner) || a.side.localeCompare(b.side));
  // Lanes first: a box must exist before the nodes placed inside it.
  const capped = more.reduce((t, m) => t + m.count, 0);
  return { graph: { nodes: [...lanes, ...kept, ...groupNodes] }, groups, folded: rep.size - capped, ...(more.length ? { more } : {}) };
}
