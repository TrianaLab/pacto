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
}
interface NHood {
  nodes?: NNode[] | null;
  edges?: NEdge[] | null;
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
    const edge: GraphEdge = {
      targetId: e.to.key,
      type: edgeType(e.relation),
      // Only a service-projection edge carries an edge-scope difference; observed-not-
      // expected reads as drift so the stylesheet tints it. Fine-grained edges carry no
      // difference (their service-scoped corroboration is shown in the drawer/legend,
      // never as edge color) so they are not tinted here.
      driftStatus: e.difference === 'observed-not-expected' ? 'drift' : undefined,
    };
    src.edges!.push(edge);
  }
  return { nodes };
}
