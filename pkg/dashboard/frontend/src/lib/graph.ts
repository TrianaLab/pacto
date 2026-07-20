/**
 * Dependency graph renderer built on Cytoscape.js (dagre layout + orthogonal
 * "taxi" edges). Interaction model: click a node to focus its up/down dependency
 * closure (everything dims but that node's ancestors + descendants); click the
 * background to clear; double-click to open the service. Returns GraphControls so
 * GraphCanvas/GraphPanel drive zoom/fit/filter the same as before.
 */
import cytoscape from 'cytoscape';
import type { Core, NodeSingular, EdgeSingular, ElementDefinition, LayoutOptions } from 'cytoscape';
import dagre from 'cytoscape-dagre';
import fcose from 'cytoscape-fcose';
import { reasonTooltip } from './format.ts';

cytoscape.use(dagre);
cytoscape.use(fcose);

const NODE_W = 164;
const NODE_H = 42;
// Resting edge opacity — kept low so a dense fan-in (many services → shared infra)
// reads as faint texture and the nodes stay legible; focus/hover lifts it.
const EDGE_REST = 0.4;

// Status / reason → palette token key, resolved to a concrete color per theme at
// render time (Cytoscape's canvas can't read CSS custom properties directly).
const STATUS_TO_PAL: Record<string, string> = {
  Compliant: 'ok', Warning: 'warn', NonCompliant: 'err', Reference: 'info', Unknown: 'neutral',
};
const REASON_TO_PAL: Record<string, string> = {
  non_oci_ref: 'neutral', auth_failed: 'err', no_semver_tags: 'warn', not_found: 'warn', discovering: 'accent',
};

export interface GraphEdge {
  targetId: string;
  required?: boolean;
  type?: string;
  lockedDigest?: string;
  lockedVersion?: string;
  driftStatus?: string;
}

export interface GraphNode {
  id: string;
  serviceName: string;
  status: string;
  version?: string;
  reason?: string;    // why unresolved: non_oci_ref, auth_failed, no_semver_tags, not_found, discovering
  edges?: GraphEdge[];
}

export interface GraphData {
  nodes: GraphNode[];
}

/** Computes the displayed label lines for a node: truncated name + version. */
export function nodeLabel(node: { id: string; serviceName?: string; version?: string }): { name: string; version: string } {
  const raw = node.serviceName || node.id;
  const name = raw.length > 18 ? raw.slice(0, 17) + '…' : raw;
  return { name, version: node.version || '' };
}

interface VersionDetail {
  name: string;
  contractStatus?: string;
  version?: string;
  dependencies?: Array<{ name?: string; ref?: string; required?: boolean }>;
}
interface FleetEntry {
  name: string;
  contractStatus?: string;
  version?: string;
}

/**
 * Build a flat graph for a single service AT a specific version, from that
 * version's declared dependencies. Used by the service detail view when a
 * historical version is selected, so the embedded graph reflects that version's
 * direct deps (e.g. a dep added in a later version) rather than the current
 * global topology. Dependency nodes are colored/labeled from the current fleet
 * (`services`) when known, else shown as external. Transitive deps, dependents
 * and blast-radius are intentionally omitted — they can't be reconstructed for a
 * past version.
 */
export function buildVersionSubgraph(detail: VersionDetail, services: FleetEntry[], version: string): GraphData {
  const byName = new Map((services || []).map((s) => [s.name, s]));
  const root: GraphNode = {
    id: detail.name,
    serviceName: detail.name,
    status: detail.contractStatus || 'Unknown',
    version: version || detail.version || '',
    edges: [],
  };
  const nodes: GraphNode[] = [root];
  const seen = new Set<string>([detail.name]);
  for (const dep of detail.dependencies || []) {
    const depName = dep.name;
    if (!depName || depName === detail.name) continue;
    root.edges!.push({ targetId: depName, required: dep.required, type: 'dependency' });
    if (seen.has(depName)) continue;
    seen.add(depName);
    const svc = byName.get(depName);
    nodes.push({
      id: depName,
      serviceName: depName,
      status: svc?.contractStatus || 'external',
      version: svc?.version || '',
      edges: [],
    });
  }
  return { nodes };
}

export interface GraphControls {
  nodes: GraphNode[];
  destroy: () => void;
  zoomIn: () => void;
  zoomOut: () => void;
  resetView: () => void;
  applyFilter: (fn: ((n: GraphNode) => boolean) | null) => void;
}

interface RenderOptions {
  onNavigate?: (name: string) => void;
  focusId?: string;
  filterFn?: (n: GraphNode) => boolean;
  /** Set of service names to persistently emphasize (e.g. owner's services). */
  focusNodes?: Set<string>;
  /** 'layered' uses a top-down dagre hierarchy; 'force' uses cose. Default 'force'. */
  layout?: 'force' | 'layered';
  // Accepted for API compatibility; the "+N" expand chip is superseded by
  // click-to-focus, so these are ignored.
  hidden?: Map<string, number>;
  onExpand?: (id: string) => void;
}

/**
 * Build Cytoscape elements from the graph data. Pure (no Cytoscape/DOM), so the
 * node/edge shape is unit-testable. Edge ids include the type because a node can
 * have both a dependency and a config/policy reference to the same target.
 */
export function buildElements(graphData: GraphData, focusId?: string): ElementDefinition[] {
  const nodes = graphData.nodes || [];
  const ids = new Set(nodes.map((n) => n.id));
  const els: ElementDefinition[] = [];
  for (const n of nodes) {
    const { name, version } = nodeLabel(n);
    els.push({
      data: {
        id: n.id,
        serviceName: n.serviceName,
        label: version ? `${name}\n${version}` : name,
        status: n.status,
        reason: n.reason || '',
        external: n.status === 'external' ? 1 : 0,
        isFocus: n.serviceName === focusId ? 1 : 0,
      },
    });
  }
  for (const n of nodes) {
    for (const e of n.edges || []) {
      if (!ids.has(e.targetId)) continue;
      const etype = e.type || 'dependency';
      els.push({
        data: {
          id: `${n.id}→${e.targetId}:${etype}`,
          source: n.id,
          target: e.targetId,
          etype,
          required: e.required ? 1 : 0,
          drift: e.driftStatus === 'drift' ? 1 : 0,
        },
      });
    }
  }
  return els;
}

/**
 * 'layered' → dagre top-down tree (good for a small, clearly-hierarchical
 * per-service view). 'force' → fCoSE: a compact 2D packing that fills the canvas
 * at a readable node size instead of collapsing a wide, shallow fleet into a thin
 * band — shared-infra hubs settle centrally. Used for the fleet + owner views.
 */
export function cyLayout(layout: 'force' | 'layered'): LayoutOptions {
  if (layout === 'layered') {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return { name: 'dagre', rankDir: 'TB', nodeSep: 45, rankSep: 70, fit: true, padding: 30 } as any;
  }
  return {
    name: 'fcose',
    animate: false,
    fit: true,
    padding: 40,
    nodeSeparation: 120,
    idealEdgeLength: 110,
    nodeRepulsion: 6000,
    packComponents: true,
    nodeDimensionsIncludeLabels: true,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any;
}

interface Palette {
  surface: string; text: string; textDim: string; border: string;
  ok: string; warn: string; err: string; info: string; neutral: string; accent: string;
}

function resolvePalette(container: HTMLElement): Palette {
  const v = (name: string, fallback: string) =>
    getComputedStyle(container).getPropertyValue(name).trim() || fallback;
  return {
    surface: v('--c-surface', '#111827'),
    text: v('--c-text', '#f1f5f9'),
    textDim: v('--c-text-3', '#818ea3'),
    border: v('--c-border', '#1e293b'),
    ok: v('--c-ok', '#34d399'),
    warn: v('--c-warn', '#fbbf24'),
    err: v('--c-err', '#f87171'),
    info: v('--c-info', '#60a5fa'),
    neutral: v('--c-neutral', '#64748b'),
    accent: v('--c-accent', '#818cf8'),
  };
}

/** Border/status color for a node, theme-aware and reason-aware for externals. */
function nodeColor(pal: Palette, status: string, reason: string): string {
  if (status === 'external') return (pal as any)[REASON_TO_PAL[reason]] || pal.neutral;
  return (pal as any)[STATUS_TO_PAL[status]] || pal.neutral;
}

/** Edge/arrow color by kind. */
function edgeColor(pal: Palette, drift: boolean, reference: boolean): string {
  if (drift) return pal.warn;
  if (reference) return pal.accent;
  return pal.textDim;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function cyStylesheet(pal: Palette, layout: 'force' | 'layered'): any[] {
  return [
    {
      selector: 'node',
      style: {
        shape: 'round-rectangle',
        width: NODE_W,
        height: NODE_H,
        'background-color': pal.surface,
        'border-color': (ele: NodeSingular) => nodeColor(pal, ele.data('status'), ele.data('reason')),
        'border-width': (ele: NodeSingular) => {
          if (ele.data('isFocus')) return 3;
          const s = ele.data('status');
          return s === 'Warning' || s === 'NonCompliant' ? 2.5 : 1.5;
        },
        label: 'data(label)',
        color: pal.text,
        'font-size': 12,
        'font-weight': 500,
        'text-valign': 'center',
        'text-halign': 'center',
        'text-wrap': 'wrap',
        'text-max-width': `${NODE_W - 18}`,
        'line-height': 1.25,
      },
    },
    {
      selector: 'edge',
      style: {
        'curve-style': layout === 'layered' ? 'taxi' : 'bezier',
        'taxi-direction': 'downward',
        'taxi-turn': 24,
        'taxi-turn-min-distance': 6,
        width: (ele: EdgeSingular) => (ele.data('required') ? 2 : 1),
        'line-color': (ele: EdgeSingular) => edgeColor(pal, !!ele.data('drift'), ele.data('etype') === 'reference'),
        'line-style': (ele: EdgeSingular) =>
          ele.data('etype') === 'reference' ? 'dashed' : ele.data('required') ? 'solid' : 'dashed',
        'target-arrow-shape': 'triangle',
        'target-arrow-color': (ele: EdgeSingular) => edgeColor(pal, !!ele.data('drift'), ele.data('etype') === 'reference'),
        'arrow-scale': 0.9,
        opacity: EDGE_REST,
      },
    },
    { selector: 'node.pacto-faded', style: { opacity: 0.12 } },
    { selector: 'edge.pacto-faded', style: { opacity: 0.04 } },
    { selector: 'node.pacto-focus', style: { 'border-color': pal.accent, 'border-width': 3 } },
    { selector: 'edge.pacto-lit', style: { opacity: 0.85, width: 2 } },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ] as any;
}

export function renderGraph(
  container: HTMLElement,
  graphData: GraphData,
  { onNavigate, focusId, filterFn, focusNodes, layout = 'force' }: RenderOptions = {},
): GraphControls {
  const nodes: GraphNode[] = (graphData.nodes || []).map((n) => ({ ...n }));
  container.innerHTML = '';
  // Accessible fallback lives in the connections table; mark the canvas as a
  // presentational application region.
  container.setAttribute('role', 'application');
  container.setAttribute('aria-label', 'Dependency graph (see the connections table for a text version)');

  const pal = resolvePalette(container);
  const baseOpts = {
    elements: buildElements(graphData, focusId),
    style: cyStylesheet(pal, layout),
    minZoom: 0.2,
    maxZoom: 3,
    boxSelectionEnabled: false,
  };
  let cy: Core;
  try {
    cy = cytoscape({ container, ...baseOpts });
  } catch {
    // No 2D canvas (e.g. the jsdom test env) — build the model headless so the
    // controls API still works and can be unit-tested; the browser always has canvas.
    cy = cytoscape({ headless: true, styleEnabled: true, ...baseOpts });
  }

  // ── Dimming state ────────────────────────────────────────────────────────
  // A node can be dimmed by three independent things; recompute the union so they
  // compose (click-focus over owner-emphasis over the status/name filter).
  const ownerSet = focusNodes && focusNodes.size ? focusNodes : null;
  let clickFocusId: string | null = null;
  let filterHidden: Set<string> | null = null;

  function ownerVisible(): Set<string> {
    // Owner's services + their direct neighbors.
    const vis = new Set<string>();
    cy.nodes().forEach((n) => { if (ownerSet!.has(n.data('serviceName'))) vis.add(n.id()); });
    for (const id of [...vis]) {
      cy.getElementById(id).neighborhood('node').forEach((n) => { vis.add(n.id()); });
    }
    return vis;
  }

  function applyDimming(): void {
    cy.elements().removeClass('pacto-faded').removeClass('pacto-lit');
    let keep: cytoscape.CollectionReturnValue | null = null;
    if (clickFocusId) {
      const n = cy.getElementById(clickFocusId);
      keep = n.union(n.successors()).union(n.predecessors());
      keep.edges().addClass('pacto-lit');
    } else if (ownerSet) {
      const ids = ownerVisible();
      const kn = cy.nodes().filter((n) => ids.has(n.id()));
      keep = kn.union(kn.connectedEdges());
    } else if (filterHidden) {
      // Filter dims the MATCHING nodes (inverse of keep).
      const faded = cy.nodes().filter((n) => filterHidden!.has(n.id()));
      faded.addClass('pacto-faded');
      faded.connectedEdges().addClass('pacto-faded');
      return;
    }
    if (keep) cy.elements().not(keep).addClass('pacto-faded');
  }

  // ── Interactions ─────────────────────────────────────────────────────────
  let lastTapId = '';
  let lastTapAt = 0;
  cy.on('tap', 'node', (evt) => {
    const n = evt.target as NodeSingular;
    const id = n.id();
    const now = Date.now();
    if (id === lastTapId && now - lastTapAt < 300) {
      // double-tap → open the service (external nodes have no page)
      if (!n.data('external') && onNavigate) onNavigate(n.data('serviceName'));
    } else {
      clickFocusId = clickFocusId === id ? null : id; // toggle focus
      applyDimming();
    }
    lastTapId = id;
    lastTapAt = now;
  });
  cy.on('tap', (evt) => {
    if (evt.target === cy) { clickFocusId = null; applyDimming(); }
  });

  function fit(): void { cy.fit(undefined, 30); }

  // Run layout, then fit + apply the resting emphasis. Works for both the sync
  // dagre layout and the async cose layout via layoutstop.
  const lay = cy.layout(cyLayout(layout));
  lay.one('layoutstop', () => {
    if (filterFn) applyFilter(filterFn);
    else applyDimming();
    if (focusId) {
      const fn = cy.nodes().filter((n) => n.data('serviceName') === focusId || n.id() === focusId);
      if (fn.nonempty()) cy.center(fn);
    }
  });
  lay.run();

  function applyFilter(fn: ((n: GraphNode) => boolean) | null): void {
    if (!fn) { filterHidden = null; applyDimming(); return; }
    filterHidden = new Set(nodes.filter((n) => fn(n)).map((n) => n.id));
    applyDimming();
  }

  function zoomBy(factor: number): void {
    cy.zoom({ level: cy.zoom() * factor, renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } });
  }

  return {
    nodes,
    destroy: () => cy.destroy(),
    zoomIn: () => zoomBy(1.4),
    zoomOut: () => zoomBy(0.7),
    resetView: () => { clickFocusId = null; applyDimming(); cy.animate({ fit: { eles: cy.elements(), padding: 30 } }, { duration: 300 }); },
    applyFilter,
  };
}

/**
 * Extract a subgraph centered on focusId via BFS.
 */
export function extractSubgraph(graphData: GraphData | null, focusId: string | null): GraphData | null {
  if (!graphData?.nodes?.length || !focusId) return null;
  const nodeMap = new Map(graphData.nodes.map((n) => [n.id, n]));
  const focus = nodeMap.get(focusId);
  if (!focus) return null;

  const visited = new Set([focusId]);
  const queue = [focusId];
  // Gather direct edges from focus + edges pointing to focus
  while (queue.length) {
    const id = queue.shift()!;
    const node = nodeMap.get(id);
    if (!node) continue;
    for (const edge of node.edges || []) {
      if (!visited.has(edge.targetId) && nodeMap.has(edge.targetId)) {
        visited.add(edge.targetId);
        queue.push(edge.targetId);
      }
    }
  }
  // Also add nodes that point TO any visited node
  for (const node of graphData.nodes) {
    for (const edge of node.edges || []) {
      if (visited.has(edge.targetId)) visited.add(node.id);
    }
  }

  const subNodes = graphData.nodes.filter((n) => visited.has(n.id));
  return subNodes.length > 1 ? { nodes: subNodes } : null;
}
