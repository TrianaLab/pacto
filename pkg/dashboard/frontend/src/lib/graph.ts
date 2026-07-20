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
// eslint-disable-next-line @typescript-eslint/no-explicit-any
import expandCollapse from 'cytoscape-expand-collapse';
import { reasonTooltip } from './format.ts';

cytoscape.use(dagre);
cytoscape.use(fcose);
// Registration can throw if already registered (HMR); ignore.
try { cytoscape.use(expandCollapse); } catch { /* already registered */ }

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
  /** 'layered' uses a top-down dagre hierarchy; 'force' uses fCoSE. Default 'force'. */
  layout?: 'force' | 'layered';
  /** nodeId → cluster label (e.g. owning team). Renders collapsible compound
   *  group boxes; groups start collapsed for an at-a-glance view. */
  groups?: Map<string, string>;
  // Accepted for API compatibility; the "+N" expand chip is superseded by
  // click-to-focus, so these are ignored.
  hidden?: Map<string, number>;
  onExpand?: (id: string) => void;
}

/** Stable, DOM-id-safe element id for a cluster/group parent. */
function groupId(label: string): string {
  return 'group:' + label.replace(/[^a-zA-Z0-9_-]/g, '_');
}

// Distinct tints for cluster boxes so teams read as visually separate regions.
const GROUP_COLORS = ['#818cf8', '#34d399', '#f472b6', '#fbbf24', '#60a5fa', '#a78bfa', '#fb923c', '#2dd4bf', '#f87171', '#c084fc'];
function groupColor(index: number): string {
  return GROUP_COLORS[index % GROUP_COLORS.length];
}

/**
 * Build Cytoscape elements from the graph data. Pure (no Cytoscape/DOM), so the
 * node/edge shape is unit-testable. Edge ids include the type because a node can
 * have both a dependency and a config/policy reference to the same target.
 */
export function buildElements(graphData: GraphData, focusId?: string, groups?: Map<string, string>): ElementDefinition[] {
  const nodes = graphData.nodes || [];
  const ids = new Set(nodes.map((n) => n.id));
  const els: ElementDefinition[] = [];
  // One compound parent per distinct cluster label present in the graph, each
  // given a stable tint so teams read as separate regions.
  const labelOf = (id: string) => (groups && groups.get(id)) || '';
  const colorOfGroup = new Map<string, string>();
  if (groups && groups.size) {
    const seen = new Map<string, string>(); // gid -> label
    for (const n of nodes) {
      const label = labelOf(n.id);
      if (label) seen.set(groupId(label), label);
    }
    [...seen.keys()].sort().forEach((gid, i) => colorOfGroup.set(gid, groupColor(i)));
    for (const [gid, label] of seen) {
      els.push({ data: { id: gid, label, isGroup: 1, groupColor: colorOfGroup.get(gid) } });
    }
  }
  for (const n of nodes) {
    const { name, version } = nodeLabel(n);
    const label = labelOf(n.id);
    els.push({
      data: {
        id: n.id,
        serviceName: n.serviceName,
        label: version ? `${name}\n${version}` : name,
        status: n.status,
        reason: n.reason || '',
        external: n.status === 'external' ? 1 : 0,
        isFocus: n.serviceName === focusId ? 1 : 0,
        parent: label ? groupId(label) : undefined,
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
    return { name: 'dagre', rankDir: 'TB', nodeSep: 45, rankSep: 70, fit: true, padding: 30, animate: true, animationDuration: 400 } as any;
  }
  return {
    name: 'fcose',
    animate: 'end',
    animationDuration: 600,
    animationEasing: 'ease-out',
    fit: true,
    padding: 40,
    nodeSeparation: 130,
    idealEdgeLength: 110,
    nodeRepulsion: 6500,
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
        'overlay-opacity': 0,
        // Smooth fades/emphasis when focus, hover or filter changes.
        'transition-property': 'opacity, border-width, background-opacity',
        'transition-duration': '0.22s',
        'transition-timing-function': 'ease-out',
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
        'transition-property': 'opacity, width, line-color',
        'transition-duration': '0.2s',
        'transition-timing-function': 'ease-out',
      },
    },
    // Compound group boxes (owner clusters): a labelled, team-tinted region.
    {
      selector: ':parent',
      style: {
        shape: 'round-rectangle',
        'background-color': 'data(groupColor)',
        'background-opacity': 0.07,
        'border-color': 'data(groupColor)',
        'border-width': 2,
        'border-opacity': 0.45,
        label: 'data(label)',
        color: 'data(groupColor)',
        'font-size': 14,
        'font-weight': 700,
        'text-valign': 'top',
        'text-halign': 'left',
        'text-margin-x': 10,
        'text-margin-y': 6,
        padding: 22,
        'transition-property': 'opacity, background-opacity',
        'transition-duration': '0.22s',
      },
    },
    // Collapsed group node (a whole team folded into one box).
    {
      selector: 'node.cy-expand-collapse-collapsed-node',
      style: {
        shape: 'round-rectangle',
        'background-color': 'data(groupColor)',
        'background-opacity': 0.16,
        'border-color': 'data(groupColor)',
        'border-width': 2,
        color: pal.text,
        'font-size': 14,
        'font-weight': 700,
        padding: 14,
      },
    },
    { selector: 'node.pacto-faded', style: { opacity: 0.1 } },
    { selector: 'edge.pacto-faded', style: { opacity: 0.03 } },
    { selector: 'node.pacto-focus', style: { 'border-color': pal.accent, 'border-width': 3 } },
    { selector: 'node.pacto-hot', style: { 'border-width': 3 } },
    { selector: 'edge.pacto-lit', style: { opacity: 0.95, width: 2.5, 'line-color': pal.accent, 'target-arrow-color': pal.accent } },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ] as any;
}

export function renderGraph(
  container: HTMLElement,
  graphData: GraphData,
  { onNavigate, focusId, filterFn, focusNodes, layout = 'force', groups }: RenderOptions = {},
): GraphControls {
  const nodes: GraphNode[] = (graphData.nodes || []).map((n) => ({ ...n }));
  const hasGroups = !!(groups && groups.size);
  container.innerHTML = '';
  // Accessible fallback lives in the connections table; mark the canvas as a
  // presentational application region.
  container.setAttribute('role', 'application');
  container.setAttribute('aria-label', 'Dependency graph (see the connections table for a text version)');

  const pal = resolvePalette(container);
  const baseOpts = {
    elements: buildElements(graphData, focusId, groups),
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

  // Collapsible owner boxes (native compound nodes + the expand-collapse extension).
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let ecApi: any = null;
  if (hasGroups) {
    try {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ecApi = (cy as any).expandCollapse({
        layoutBy: { name: 'fcose', animate: 'end', animationDuration: 500, fit: true, padding: 40, randomize: false, nodeDimensionsIncludeLabels: true },
        fisheye: false, animate: true, animationDuration: 400, undoable: false, cueEnabled: true,
      });
    } catch { ecApi = null; }
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

  // A node plus its full up/down dependency closure and the group boxes around them.
  function closureOf(n: cytoscape.NodeSingular): cytoscape.CollectionReturnValue {
    const c = n.union(n.successors()).union(n.predecessors());
    return c.union(c.ancestors());
  }

  function applyDimming(): void {
    cy.elements().removeClass('pacto-faded').removeClass('pacto-lit').removeClass('pacto-hot');
    let keep: cytoscape.CollectionReturnValue | null = null;
    if (clickFocusId) {
      const n = cy.getElementById(clickFocusId);
      keep = closureOf(n);
      keep.edges().addClass('pacto-lit');
      n.addClass('pacto-hot');
    } else if (ownerSet) {
      const ids = ownerVisible();
      const kn = cy.nodes().filter((n) => ids.has(n.id()));
      keep = kn.union(kn.connectedEdges()).union(kn.ancestors());
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
    // Group boxes: tap a collapsed team to expand it, or an expanded box to fold.
    if (ecApi) {
      if (n.hasClass('cy-expand-collapse-collapsed-node')) { ecApi.expand(n); return; }
      if (n.isParent()) { ecApi.collapse(n); return; }
    }
    const id = n.id();
    const now = Date.now();
    if (id === lastTapId && now - lastTapAt < 300) {
      // double-tap → open the service (external nodes have no page)
      if (!n.data('external') && onNavigate) onNavigate(n.data('serviceName'));
    } else {
      clickFocusId = clickFocusId === id ? null : id; // toggle focus
      applyDimming();
      // Smoothly move the camera to the focused closure (or back to the whole graph).
      if (clickFocusId) cy.animate({ fit: { eles: closureOf(n), padding: 70 } }, { duration: 450, easing: 'ease-in-out' });
      else cy.animate({ fit: { eles: cy.elements(), padding: 30 } }, { duration: 400, easing: 'ease-in-out' });
    }
    lastTapId = id;
    lastTapAt = now;
  });
  cy.on('tap', (evt) => {
    if (evt.target === cy && clickFocusId) {
      clickFocusId = null;
      applyDimming();
      cy.animate({ fit: { eles: cy.elements(), padding: 30 } }, { duration: 400, easing: 'ease-in-out' });
    }
  });

  // Hover: smoothly spotlight a service's dependency closure without committing to
  // it (click pins it). Skipped while a click-focus or owner emphasis is active.
  cy.on('mouseover', 'node', (evt) => {
    container.style.cursor = 'pointer';
    const n = evt.target as NodeSingular;
    if (clickFocusId || ownerSet || filterHidden || n.isParent() || n.hasClass('cy-expand-collapse-collapsed-node')) return;
    const keep = closureOf(n);
    cy.elements().not(keep).addClass('pacto-faded');
    keep.edges().addClass('pacto-lit');
    n.addClass('pacto-hot');
  });
  cy.on('mouseout', 'node', () => {
    container.style.cursor = 'default';
    if (clickFocusId || ownerSet || filterHidden) return;
    cy.elements().removeClass('pacto-faded').removeClass('pacto-lit').removeClass('pacto-hot');
  });

  // Run layout, then fit + apply the resting emphasis. Works for both the sync
  // dagre layout and the async cose layout via layoutstop.
  const lay = cy.layout(cyLayout(layout));
  lay.one('layoutstop', () => {
    // Default to the EXPANDED clustered view — every service visible inside its
    // team region — which is far richer than a few folded boxes. Tap a box to fold
    // a team on demand.
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
