/**
 * Pure adapter from a bounded product ProductNeighborhood to the GraphData the shared
 * Cytoscape engine (lib/graph.ts) renders. It invents no nodes or edges: it is a
 * structural projection of the backend answer, so the visual topology shows EXACTLY
 * what the backend returned (every returned node, every returned edge between returned
 * nodes) and never re-infers relationships in the frontend (requirement I).
 */
import type { GraphData, GraphNode, GraphEdge } from './graph.ts';

// Structural sub-shapes of the generated ProductNeighborhood (the generated SDK types
// are the source of truth; these read only the fields the adapter needs).
interface NRef {
  kind: string;
  key: string;
  label?: string;
  status?: string;
}
interface NNode {
  ref: NRef;
  status?: string;
  focus?: boolean;
}
interface NEdge {
  from: NRef;
  to: NRef;
  relation: string;
  difference?: string;
  serviceCorroboration?: string;
}
interface NHood {
  nodes?: NNode[] | null;
  edges?: NEdge[] | null;
}

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
export function neighborhoodToGraph(nb: NHood | null | undefined): GraphData {
  const nodes: GraphNode[] = [];
  const byKey = new Map<string, GraphNode>();
  for (const n of nb?.nodes ?? []) {
    const g: GraphNode = {
      id: n.ref.key,
      serviceName: n.ref.label || n.ref.key,
      status: n.status || n.ref.status || 'Unknown',
      kind: nodeKind(n.ref.kind),
      edges: [],
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
