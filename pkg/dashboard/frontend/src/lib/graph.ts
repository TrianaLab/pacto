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
import { wrapWideRanks } from './layout.ts';
import { prefersReducedMotion } from './chartkit.ts';

cytoscape.use(dagre);
cytoscape.use(fcose);
// Registration can throw if already registered (HMR); ignore.
try { cytoscape.use(expandCollapse); } catch { /* already registered */ }

const NODE_W = 164;
const NODE_H = 42;
// Resting edge opacity — very faint so the whole fleet reads as a calm map of
// nodes (global overview) without the edge cloud; focus/hover lights the relevant
// edges directionally.
const EDGE_REST = 0.07;

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
  // edgeState is the backend-provided reconciliation state the canvas renders as a
  // real visual distinction (matched / expected-not-observed / drift / insufficient),
  // never re-inferred. Empty = a plain declared edge (no comparison in view). See
  // lib/neighborhoodGraph.ts, which derives it from difference/serviceCorroboration.
  edgeState?: string;
}

export interface GraphNode {
  id: string;
  serviceName: string;
  status: string;
  version?: string;
  reason?: string;    // why unresolved: non_oci_ref, auth_failed, no_semver_tags, not_found, discovering
  // kind distinguishes what a node represents so the renderer can style it and the
  // viewer is never misled about the layer: 'target' is a deployed instance,
  // 'service' is a logical service (incl. a dependency-service aggregate in the
  // target perspective), 'revision' is a content-addressed revision. Absent = service.
  kind?: 'service' | 'revision' | 'target';
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
  /** Fit the whole graph in view without clearing the pinned focus (ephemeral). */
  fit: () => void;
  /** Update presentation fields (node status/label/kind, edge state) in place without
   *  a relayout, for a same-topology refresh. */
  patchData: (graphData: GraphData) => void;
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
  /** Fired on single-tap pin (serviceName) and unpin / background tap (null). */
  onSelect?: (serviceName: string | null) => void;
  /** Fired on single-tap with the node's stable id (null on unpin/background). The
   *  product Operational Graph uses this (not onSelect) so a node is identified by its
   *  canonical (kind,key) id, which is unique across mixed node kinds. */
  onSelectNode?: (id: string | null) => void;
  /** Fired when an edge is tapped, with the edge's stable id (null on background). */
  onSelectEdge?: (id: string | null) => void;
  /** When true, single-tap opens the service (embedded graphs). Default: false = spotlight. */
  tapToOpen?: boolean;
  /** 'visible' shows a bounded neighborhood's edges at rest with arrowheads; 'faint'
   *  (default) keeps the whole-fleet map's calm faint-until-focus edges. */
  edgeStyle?: 'faint' | 'visible';
  /** When false, the initial focus node is highlighted but NOT auto-spotlighted (so a
   *  bounded neighborhood shows all its edges at rest, dependency vs runs distinct,
   *  instead of dimming to the focus's cone). Default true (whole-fleet behavior). */
  autoSpotlightFocus?: boolean;
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
        kind: n.kind || 'service',
        external: n.status === 'external' ? 1 : 0,
        isFocus: n.serviceName === focusId || n.id === focusId ? 1 : 0,
        parent: label ? groupId(label) : undefined,
      },
    });
  }
  for (const n of nodes) {
    for (const e of n.edges || []) {
      if (!ids.has(e.targetId)) continue;
      const etype = e.type || 'dependency';
      // The reconciliation state drives a real edge visual (see cyStylesheet). A
      // legacy driftStatus folds into the same state vocabulary so both the whole-fleet
      // graph and the product neighborhood share one grammar.
      const state = e.edgeState || (e.driftStatus === 'drift' ? 'drift' : '');
      els.push({
        data: {
          id: `${n.id}→${e.targetId}:${etype}`,
          source: n.id,
          target: e.targetId,
          etype,
          required: e.required ? 1 : 0,
          drift: state === 'drift' ? 1 : 0,
          state,
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
    // Top-down dependency tree: roots on top → services → shared-infra sinks at the
    // bottom. fit/animate off here — renderGraph wraps over-wide levels first, then
    // fits, so a fat level doesn't stretch the tree into a band.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return { name: 'dagre', rankDir: 'TB', nodeSep: 24, rankSep: 80, fit: false, animate: false } as any;
  }
  return {
    name: 'fcose',
    // Honor prefers-reduced-motion: settle instantly rather than animating the layout.
    animate: prefersReducedMotion() ? false : 'end',
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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function cyStylesheet(pal: Palette, layout: 'force' | 'layered', edgeStyle: 'faint' | 'visible' = 'faint'): any[] {
  // A bounded focused neighborhood (edgeStyle 'visible') shows its edges at rest with
  // arrowheads, because there are only a handful and the whole point is to read them.
  // The whole-fleet map ('faint') keeps edges near-invisible until a node is focused so
  // it reads as a calm scatter, not an edge cloud.
  const restOpacity = layout === 'layered' ? 0.35 : edgeStyle === 'visible' ? 0.55 : EDGE_REST;
  const restArrow = layout === 'layered' || edgeStyle === 'visible' ? 'triangle' : 'none';
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
        'transition-duration': '0.12s',
        'transition-timing-function': 'ease-out',
      },
    },
    {
      // A deployed instance (target perspective) reads as an ellipse so it is never
      // mistaken for a logical service/revision node (round-rectangle). This keeps
      // the physical vs logical distinction honest at a glance.
      selector: "node[kind='target']",
      style: { shape: 'ellipse' },
    },
    {
      // An immutable revision node reads as a dashed-border round-rectangle, matching
      // the legend swatch, so it is distinguishable from a logical service (solid
      // border) without relying on color.
      selector: "node[kind='revision']",
      style: { 'border-style': 'dashed' },
    },
    {
      selector: 'edge',
      style: {
        // Layered = the dependency TREE: orthogonal taxi edges flowing top→down with
        // arrowheads, visible at rest so the structure reads instantly. Force = a
        // calm scatter where edges stay faint until a node is focused.
        'curve-style': layout === 'layered' ? 'taxi' : 'bezier',
        'taxi-direction': 'downward',
        'taxi-turn': 20,
        'taxi-turn-min-distance': 5,
        'control-point-step-size': 24,
        width: 1,
        'line-color': pal.textDim,
        'line-style': (ele: EdgeSingular) => (ele.data('etype') === 'reference' || ele.data('etype') === 'runs' ? 'dashed' : 'solid'),
        'target-arrow-shape': restArrow,
        'target-arrow-color': pal.textDim,
        'arrow-scale': 0.85,
        opacity: restOpacity,
        'transition-property': 'opacity, width, line-color',
        'transition-duration': '0.12s',
        'transition-timing-function': 'ease-out',
      },
    },
    // Reconciliation state on a dependency edge, rendered as a REAL visual distinction
    // so every legend item maps to something the canvas actually draws (requirement,
    // Part 6). The backend state is rendered verbatim, never re-inferred: color carries
    // tone and line width/style carry confidence, so the states are distinguishable
    // without relying on color alone.
    {
      // matched / corroborated: declared intent backed by observation. Confident solid
      // line in the ok tone.
      selector: "edge[state='matched']",
      style: { 'line-color': pal.ok, 'target-arrow-color': pal.ok, width: 2 },
    },
    {
      // expected-not-observed: declared but not witnessed in the window (not proof it
      // is unused). Info tone, kept solid.
      selector: "edge[state='expected-not-observed']",
      style: { 'line-color': pal.info, 'target-arrow-color': pal.info },
    },
    {
      // drift (observed-not-expected): observed at runtime but never declared. The
      // attention state: warn tone and a thicker line.
      selector: "edge[state='drift']",
      style: { 'line-color': pal.warn, 'target-arrow-color': pal.warn, width: 2.5 },
    },
    {
      // insufficient: declared with no observation to reconcile against. A dotted line
      // in the neutral tone reads as "unverified" without color alone.
      selector: "edge[state='insufficient']",
      style: { 'line-color': pal.neutral, 'target-arrow-color': pal.neutral, 'line-style': 'dotted' },
    },
    {
      // A "runs" edge (a deployment runs a revision) is a structural identity link,
      // not a declared-vs-observed dependency: render it dashed in the info tone so it
      // reads distinctly from a solid dependency edge (never color alone -- the dash
      // and the legend carry the distinction too). Declared last so it wins for runs
      // edges (which never carry a reconciliation state).
      selector: "edge[etype='runs']",
      style: { 'line-color': pal.info, 'target-arrow-color': pal.info, 'line-style': 'dashed' },
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
        'transition-duration': '0.12s',
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
    { selector: 'node.pacto-faded', style: { opacity: 0.12 } },
    { selector: 'edge.pacto-faded', style: { opacity: 0.02 } },
    { selector: 'node.pacto-focus', style: { 'border-color': pal.accent, 'border-width': 3 } },
    { selector: 'node.pacto-hot', style: { 'border-width': 3.5 } },
    // Directional highlight: a service's DEPENDENCIES (what it needs) light up in
    // accent with arrows pointing away; its DEPENDENTS (blast radius) light up warm
    // with arrows pointing in. Only one node's edges show at once → no overlap.
    {
      selector: 'edge.pacto-dep',
      style: { opacity: 0.95, width: 2.5, 'line-color': pal.accent, 'target-arrow-color': pal.accent, 'target-arrow-shape': 'triangle', 'line-style': 'solid', 'z-index': 10 },
    },
    {
      selector: 'edge.pacto-dependent',
      style: { opacity: 0.9, width: 2.5, 'line-color': pal.warn, 'target-arrow-color': pal.warn, 'target-arrow-shape': 'triangle', 'line-style': 'solid', 'z-index': 10 },
    },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ] as any;
}

export function renderGraph(
  container: HTMLElement,
  graphData: GraphData,
  { onNavigate, focusId, filterFn, focusNodes, layout = 'force', groups, onSelect, onSelectNode, onSelectEdge, tapToOpen, edgeStyle = 'faint', autoSpotlightFocus = true }: RenderOptions = {},
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
    style: cyStylesheet(pal, layout, edgeStyle),
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
      const reduce = prefersReducedMotion();
      ecApi = (cy as any).expandCollapse({
        layoutBy: { name: 'fcose', animate: reduce ? false : 'end', animationDuration: 500, fit: true, padding: 40, randomize: false, nodeDimensionsIncludeLabels: true },
        fisheye: false, animate: !reduce, animationDuration: 400, undoable: false, cueEnabled: true,
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

  const HL = 'pacto-faded pacto-dep pacto-dependent pacto-hot';

  // Directional spotlight — the one interaction used EVERYWHERE (fleet, owner,
  // service detail) so the graph behaves identically wherever it appears. A
  // service's DEPENDENCIES light up in accent (arrows out), its DEPENDENTS / blast
  // radius in warm (arrows in), everything else fades. Only this node's edges show,
  // so nothing overlaps.
  function spotlight(n: cytoscape.NodeSingular): void {
    const succ = n.successors();
    const pred = n.predecessors();
    const keep = n.union(succ).union(pred);
    // batch → Cytoscape recomputes style once for the whole change, not per class op
    cy.batch(() => {
      cy.elements().not(keep.union(keep.ancestors())).addClass('pacto-faded');
      succ.edges().addClass('pacto-dep');
      pred.edges().addClass('pacto-dependent');
      n.addClass('pacto-hot');
    });
  }

  // Resting emphasis (no hover/pin): owner view dims to the owner's services; the
  // status/name filter dims non-matches; otherwise the calm full map.
  function applyDimming(): void {
    cy.batch(() => {
      cy.elements().removeClass(HL);
      if (clickFocusId) { return; }
      if (ownerSet) {
        const ids = ownerVisible();
        const kn = cy.nodes().filter((n) => ids.has(n.id()));
        const keep = kn.union(kn.connectedEdges()).union(kn.ancestors());
        cy.elements().not(keep).addClass('pacto-faded');
      } else if (filterHidden) {
        const faded = cy.nodes().filter((n) => filterHidden!.has(n.id()));
        faded.addClass('pacto-faded');
        faded.connectedEdges().addClass('pacto-faded');
      }
    });
    if (clickFocusId) spotlight(cy.getElementById(clickFocusId));
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
    const isDouble = id === lastTapId && now - lastTapAt < 300;
    lastTapId = id;
    lastTapAt = now;
    if (isDouble) {
      // double-tap → open the service page (external nodes have none)
      if (!n.data('external') && onNavigate) onNavigate(n.data('serviceName'));
      return;
    }
    if (tapToOpen && !n.data('external') && onNavigate) { onNavigate(n.data('serviceName')); return; }
    // Single-tap → PIN this service's directional spotlight and gently center it,
    // keeping the whole map in view (no hard zoom) so the global picture stays.
    clickFocusId = clickFocusId === id ? null : id;
    applyDimming();
    onSelect?.(clickFocusId ? n.data('serviceName') : null);
    onSelectNode?.(clickFocusId ? id : null);
    if (clickFocusId) {
      if (prefersReducedMotion()) cy.center(n);
      else cy.animate({ center: { eles: n } }, { duration: 250, easing: 'ease-in-out' });
    }
  });
  // Edge tap: open the relationship's quick-inspection drawer (the product graph
  // reads the backend-authoritative edge; it never navigates away automatically).
  cy.on('tap', 'edge', (evt) => {
    const e = evt.target as EdgeSingular;
    onSelectEdge?.(e.id());
  });
  cy.on('tap', (evt) => {
    if (evt.target !== cy) return;
    onSelectEdge?.(null);
    if (clickFocusId) { clickFocusId = null; applyDimming(); onSelect?.(null); onSelectNode?.(null); }
  });

  // Hover previews the spotlight (click pins it). A pinned focus takes precedence.
  cy.on('mouseover', 'node', (evt) => {
    container.style.cursor = 'pointer';
    const n = evt.target as NodeSingular;
    if (clickFocusId || n.isParent() || n.hasClass('cy-expand-collapse-collapsed-node')) return;
    cy.batch(() => cy.elements().removeClass(HL));
    spotlight(n);
  });
  cy.on('mouseout', 'node', () => {
    container.style.cursor = 'default';
    if (clickFocusId) return;
    applyDimming(); // restore the resting owner/filter emphasis (batched, clears HL)
  });

  // Pin the initial focus (service-detail page) before the first layout, unless the
  // caller wants the focus highlighted without dimming to its cone (bounded
  // neighborhood: show every edge at rest, dependency vs runs distinct).
  if (focusId && autoSpotlightFocus) {
    const fn = cy.nodes().filter((n) => n.data('serviceName') === focusId || n.id() === focusId);
    if (fn.nonempty()) clickFocusId = fn.first().id();
  }

  // fitView fits the whole graph in view, honoring prefers-reduced-motion (an instant
  // fit rather than an animated pan/zoom).
  function fitView(duration: number): void {
    if (prefersReducedMotion()) cy.fit(cy.elements(), 30);
    else cy.animate({ fit: { eles: cy.elements(), padding: 30 } }, { duration, easing: 'ease-out' });
  }

  function layoutAndFit(): void {
    const l = cy.layout(cyLayout(layout));
    l.one('layoutstop', () => {
      // Layered tree: fold any over-wide level into sub-rows sized to the CURRENT
      // canvas width, so one big level can't stretch the tree into a band.
      if (layout === 'layered') {
        const pos = new Map<string, { x: number; y: number }>();
        cy.nodes().forEach((n) => { pos.set(n.id(), { x: n.position('x'), y: n.position('y') }); });
        wrapWideRanks(pos, { nodeW: NODE_W, nodeH: NODE_H, nodesep: 24, ranksep: 80, maxWidth: (cy.width() || 1000) - 60 });
        cy.batch(() => cy.nodes().forEach((n) => { const p = pos.get(n.id()); if (p) n.position(p); }));
      }
      fitView(250);
      if (filterFn) applyFilter(filterFn);
      else applyDimming();
    });
    l.run();
  }

  // Lay out only once the container has a real size — cy often initialises before
  // the container is measured, which produced the "renders wrong until you click"
  // bug (a click forced cy to re-measure). The observer also re-fits/re-wraps on
  // later resizes (window, panel), unless a focus is pinned.
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let firstLayout = true;
  let ro: ResizeObserver | undefined;
  if (typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect?.width || cy.width();
      cy.resize();
      if (firstLayout) { if (w > 0) { firstLayout = false; layoutAndFit(); } return; }
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        if (clickFocusId) return; // don't disrupt a pinned focus
        if (layout === 'layered') layoutAndFit();
        else fitView(200);
      }, 150);
    });
    ro.observe(container);
  } else {
    layoutAndFit(); // no ResizeObserver (jsdom tests / very old browsers)
  }

  function applyFilter(fn: ((n: GraphNode) => boolean) | null): void {
    if (!fn) { filterHidden = null; applyDimming(); return; }
    filterHidden = new Set(nodes.filter((n) => fn(n)).map((n) => n.id));
    applyDimming();
  }

  function zoomBy(factor: number): void {
    cy.zoom({ level: cy.zoom() * factor, renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } });
  }

  // patchData updates the mutable PRESENTATION fields of the existing elements in place
  // (node status/label/kind, edge reconciliation state), WITHOUT a relayout, so a
  // background refresh that changes only semantics (e.g. Compliant -> NonCompliant, or a
  // difference verdict) restyles the canvas immediately instead of leaving it stale. It
  // assumes the topology is unchanged (same node ids and edge ids); the caller rebuilds
  // when the topology changes. Cytoscape restyles an element when its data changes, so
  // the status/kind/state selectors re-apply.
  function patchData(next: GraphData): void {
    const byId = new Map((next.nodes || []).map((n) => [n.id, n]));
    cy.batch(() => {
      for (const cn of cy.nodes()) {
        const n = byId.get(cn.id());
        if (!n) continue;
        const { name, version } = nodeLabel(n);
        cn.data('status', n.status);
        cn.data('label', version ? `${name}\n${version}` : name);
        cn.data('serviceName', n.serviceName);
        cn.data('kind', n.kind || 'service');
      }
      for (const n of next.nodes || []) {
        for (const e of n.edges || []) {
          const etype = e.type || 'dependency';
          const ce = cy.getElementById(`${n.id}→${e.targetId}:${etype}`);
          if (ce.empty()) continue;
          const state = e.edgeState || (e.driftStatus === 'drift' ? 'drift' : '');
          ce.data('state', state);
          ce.data('drift', state === 'drift' ? 1 : 0);
        }
      }
    });
  }

  return {
    nodes,
    destroy: () => { ro?.disconnect(); clearTimeout(resizeTimer); cy.destroy(); },
    zoomIn: () => zoomBy(1.4),
    zoomOut: () => zoomBy(0.7),
    resetView: () => { clickFocusId = null; applyDimming(); fitView(300); },
    fit: () => fitView(250),
    patchData,
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
