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
import { wrapWideRanks } from './layout.ts';
// motion.ts, not chartkit.ts: chartkit imports d3 at module scope, and the graph chunk
// has no business pulling a charting library in for one media query.
import { MOTION, prefersReducedMotion } from './motion.ts';
import { statusTone } from './format.ts';

cytoscape.use(dagre);
cytoscape.use(fcose);

const NODE_W = 164;
const NODE_H = 42;
// fCoSE's own animation (see cyLayout) plus the fit it ends with. Nothing the layout does
// to the viewport is a choice the reader made.
const LAYOUT_SETTLE_MS = 900;
// Resting edge opacity — very faint so the whole fleet reads as a calm map of
// nodes (global overview) without the edge cloud; focus/hover lights the relevant
// edges directionally.
const EDGE_REST = 0.07;

// Reason → palette token key, resolved to a concrete color per theme at render time
// (Cytoscape's canvas can't read CSS custom properties directly). Status has no table
// here: it reads format.ts's, so a node cannot be a different colour from the badge on
// the row that names it. The hand-written one omitted Invalid and NotEvaluated, so both
// drew grey, and toned Unknown grey where every other surface toned it amber.
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

/** GraphRenderError is thrown when the VISUAL Cytoscape renderer fails to initialize in
 *  an environment that CAN paint (a real browser). It is never thrown in a non-painting
 *  environment (unit tests build the model headless on purpose, see canPaint2D). The view
 *  catches it to show an explicit render-error state and keep the text alternative, rather
 *  than a silently-empty canvas that would still pass tests. */
export class GraphRenderError extends Error {
  constructor(cause?: unknown) {
    super('The visual graph renderer failed to initialize.');
    this.name = 'GraphRenderError';
    if (cause !== undefined) (this as { cause?: unknown }).cause = cause;
  }
}

/** GraphDiagnostics is a narrowly-scoped, read-only readiness snapshot of a rendered
 *  graph: whether it is headless, its node/edge counts, and how many nodes/edges have real
 *  rendered geometry. It exposes only what is already visible on the canvas (never internal
 *  Cytoscape state), so the browser acceptance can prove a non-headless renderer actually
 *  painted a topology rather than just mounting an empty container. */
export interface GraphDiagnostics {
  headless: boolean;
  nodeCount: number;
  edgeCount: number;
  nodesWithBox: number;
  edgesRendered: number;
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
  /** Fade the categories the legend has switched off, by legend key ('kind:revision',
   *  'rel:runs', 'state:drift', ...). Additive: it narrows whatever emphasis is already
   *  applied and never overrides a pinned focus. Pass null or an empty set to clear. */
  applyLegendFilter: (hidden: Set<string> | null) => void;
  /** Pin (or, with null, unpin) a node's directional spotlight and center it, exactly as
   *  a tap on the canvas does. Fires no selection callback: the caller already knows. */
  focusNode: (id: string | null) => void;
  /** Read-only readiness snapshot of the rendered graph (see GraphDiagnostics). */
  diagnostics: () => GraphDiagnostics;
  /** Reconcile a CHANGED topology in place: surviving node ids keep their exact
   *  positions, vanished elements are removed, and only genuinely new nodes are laid
   *  out (near their neighbors). The viewport is left alone. */
  applyTopology: (graphData: GraphData) => void;
  /** Re-read the palette from CSS custom properties and re-apply the stylesheet. Every
   *  colour the canvas draws was resolved by getComputedStyle at init, so a theme toggle
   *  leaves the canvas painted in the old theme while the page around it changes — the
   *  one place the dashboard visibly stops being one product. Style only: no relayout,
   *  no viewport change, no element data touched. */
  restyle: () => void;
  /** Discard the current arrangement: run a fresh layout from scratch and fit. This is
   *  NOT the same operation as fit(), which only re-frames what is already arranged. */
  resetLayout: () => void;
  /** The current spatial state: every node's model position plus pan/zoom. This is what
   *  a caller persists; it contains presentation coordinates only, no semantics. */
  spatialState: () => SpatialState;
}

/** SpatialState is the graph's presentation geometry: where the user put things and how
 *  they are looking at it. It carries no semantic data — a stale or foreign one can only
 *  ever misplace a node, never misreport a status. */
export interface SpatialState {
  positions: Record<string, { x: number; y: number }>;
  pan: { x: number; y: number };
  zoom: number;
}

interface RenderOptions {
  onNavigate?: (name: string) => void;
  focusId?: string;
  filterFn?: (n: GraphNode) => boolean;
  /** Set of service names to persistently emphasize (e.g. owner's services). */
  focusNodes?: Set<string>;
  /** 'layered' uses a top-down dagre hierarchy; 'force' uses fCoSE. Default 'force'. */
  layout?: 'force' | 'layered';
  /** Fired on single-tap pin (serviceName) and unpin / background tap (null). */
  onSelect?: (serviceName: string | null) => void;
  /** Fired on single-tap with the node's stable id (null on unpin/background). The
   *  product Operational Graph uses this (not onSelect) so a node is identified by its
   *  canonical (kind,key) id, which is unique across mixed node kinds. */
  onSelectNode?: (id: string | null) => void;
  /** Fired when an edge is tapped, with the edge's stable id (null on background). */
  onSelectEdge?: (id: string | null) => void;
  /** Fired once the first layout settles, with a read-only GraphDiagnostics snapshot. The
   *  view publishes it as a stable readiness seam for the visual-graph browser acceptance. */
  onReady?: (d: GraphDiagnostics) => void;
  /** When true, single-tap opens the service (embedded graphs). Default: false = spotlight. */
  tapToOpen?: boolean;
  /** 'visible' shows a bounded neighborhood's edges at rest with arrowheads; 'faint'
   *  (default) keeps the whole-fleet map's calm faint-until-focus edges. */
  edgeStyle?: 'faint' | 'visible';
  /** When false, the initial focus node is highlighted but NOT auto-spotlighted (so a
   *  bounded neighborhood shows all its edges at rest, dependency vs runs distinct,
   *  instead of dimming to the focus's cone). Default true (whole-fleet behavior). */
  autoSpotlightFocus?: boolean;
  /** Previously-saved node positions (node id -> model position) to restore instead of
   *  laying out. Ids that are not in the current graph are ignored, and nodes with no
   *  saved position are laid out around the restored ones — so a stale saved state can
   *  never make the graph unusable, only partially pre-arranged. */
  savedPositions?: Record<string, { x: number; y: number }>;
  /** Previously-saved pan/zoom, restored after the graph is ready. Restoring a viewport
   *  is only meaningful together with savedPositions; on its own it is ignored. */
  savedViewport?: { pan: { x: number; y: number }; zoom: number } | null;
  /** Fired (debounced) whenever the user changes the spatial state: dragging a node,
   *  panning, or zooming. The caller decides whether and where to persist it. */
  onSpatialChange?: (s: SpatialState) => void;
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
        kind: n.kind || 'service',
        external: n.status === 'external' ? 1 : 0,
        isFocus: n.serviceName === focusId || n.id === focusId ? 1 : 0,
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
 * What each legend entry stands for on the canvas.
 *
 * The legend already claimed to be a key to real distinctions; this is the same claim
 * written as a selector, so switching an entry off can only ever dim the thing that entry
 * describes. Keys are the legend's, values are the element data buildElements emits —
 * note `etype`, not `type`: the data field is renamed on the way in because `type` is
 * taken, and a selector written from the wire vocabulary would match nothing at all and
 * fail silently as "the filter does nothing".
 */
export const LEGEND_SELECTORS: Record<string, string> = {
  'kind:service': 'node[kind = "service"]',
  'kind:revision': 'node[kind = "revision"]',
  'kind:target': 'node[kind = "target"]',
  'rel:dependency': 'edge[etype = "dependency"]',
  'rel:runs': 'edge[etype = "runs"]',
  'state:matched': 'edge[state = "matched"]',
  'state:expected-not-observed': 'edge[state = "expected-not-observed"]',
  'state:drift': 'edge[state = "drift"]',
  'state:insufficient': 'edge[state = "insufficient"]',
};

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
    // fCoSE seeds from random positions by default, so the SAME graph laid out twice
    // lands somewhere different each time. That turns every refresh into a visible
    // reshuffle and makes "the layout was preserved" unprovable. Seeded from the
    // existing positions instead, the same input produces the same arrangement.
    randomize: false,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any;
}

interface Palette {
  surface: string; text: string; textDim: string; border: string;
  ok: string; warn: string; err: string; info: string; neutral: string; accent: string;
}

/** canPaint2D feature-detects a real 2D canvas in the container's document. A browser has
 *  one; jsdom (unit tests, no `canvas` package) does not. This selects the headless model
 *  ONLY for a genuine non-painting environment, WITHOUT a broad try/catch around the
 *  Cytoscape init that would also swallow a real visual-renderer failure and leave an empty
 *  container behind (the silent-fallback bug this replaces). */
function canPaint2D(doc: Document): boolean {
  try {
    return !!doc.createElement('canvas').getContext('2d');
  } catch {
    return false;
  }
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
  return pal[statusTone(status)] || pal.neutral;
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
        // Smooth fades/emphasis when focus, hover or filter changes. The two colours are
        // here for restyle(): a theme toggle cross-fades the canvas with the page instead
        // of snapping a frame behind it.
        'transition-property': 'opacity, border-width, background-opacity, background-color, border-color',
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
        'transition-property': 'opacity, width, line-color, target-arrow-color',
        'transition-duration': '0.12s',
        'transition-timing-function': 'ease-out',
      },
    },
    // Reconciliation state on a dependency edge, rendered as a REAL visual distinction
    // so every legend item maps to something the canvas actually draws. The backend
    // state is rendered verbatim, never re-inferred: color carries
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
    // The node the page is ABOUT, marked once and permanently. A 3px border is the same
    // 3px the hover state uses, so on arrival the subject of the graph was the one thing
    // the graph did not point at. A standing halo says it without moving: no pulse, so
    // there is no reduced-motion branch and nothing for getAnimations() to wait on.
    {
      selector: 'node[isFocus = 1]',
      style: {
        'underlay-color': pal.accent,
        'underlay-opacity': 0.22,
        'underlay-padding': 9,
        'underlay-shape': 'round-rectangle',
      },
    },
    { selector: 'node.pacto-faded', style: { opacity: 0.12 } },
    { selector: 'edge.pacto-faded', style: { opacity: 0.02 } },
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
    // Declared last so it wins over faded AND over the directional highlight: the edge
    // the pointer is on is the one the reader is asking about, and at rest it can be a
    // 1px hairline at 0.07 opacity -- not something you can tell you are pointing at.
    { selector: 'edge.pacto-edge-hot', style: { opacity: 1, width: 3.5, 'z-index': 20 } },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ] as any;
}

export function renderGraph(
  container: HTMLElement,
  graphData: GraphData,
  { onNavigate, focusId, filterFn, focusNodes, layout = 'force', onSelect, onSelectNode, onSelectEdge, onReady, tapToOpen, edgeStyle = 'faint', autoSpotlightFocus = true, savedPositions, savedViewport, onSpatialChange }: RenderOptions = {},
): GraphControls {
  const nodes: GraphNode[] = (graphData.nodes || []).map((n) => ({ ...n }));
  container.innerHTML = '';
  // The canvas is a VISUAL representation; the keyboard/screen-reader model is the text
  // alternative (the connections table / relationships list). Describe it as an image
  // rather than declaring an incomplete role="application". A caller
  // that pre-set a more specific aria-label (the neighborhood graph) keeps it.
  container.setAttribute('role', 'img');
  if (!container.getAttribute('aria-label')) {
    container.setAttribute('aria-label', 'Dependency graph (visual). See the connections table for a text version.');
  }

  const pal = resolvePalette(container);
  const baseOpts = {
    elements: buildElements(graphData, focusId),
    style: cyStylesheet(pal, layout, edgeStyle),
    minZoom: 0.2,
    maxZoom: 3,
    boxSelectionEnabled: false,
  };
  const headlessMode = !canPaint2D(container.ownerDocument);
  let cy: Core;
  if (headlessMode) {
    // Non-painting environment (jsdom unit tests, very old browsers): build the model
    // headless so the controls API is still unit-testable. This is an explicit
    // environment decision, never a fallback that hides a real browser render failure.
    cy = cytoscape({ headless: true, styleEnabled: true, ...baseOpts });
  } else {
    // A painting environment (a real browser): the VISUAL renderer MUST succeed. If it
    // throws, surface it as a typed GraphRenderError so the view shows an honest
    // render-error state plus the text alternative, never a silently-empty canvas.
    try {
      cy = cytoscape({ container, ...baseOpts });
    } catch (e) {
      throw new GraphRenderError(e);
    }
  }

  // ── Dimming state ────────────────────────────────────────────────────────
  // A node can be dimmed by four independent things; recompute the union so they
  // compose (legend filter over click-focus over owner-emphasis over the status/name
  // filter).
  const ownerSet = focusNodes && focusNodes.size ? focusNodes : null;
  let clickFocusId: string | null = null;
  let filterHidden: Set<string> | null = null;
  let legendHidden: Set<string> | null = null;

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
      cy.elements().not(keep).addClass('pacto-faded');
      succ.edges().addClass('pacto-dep');
      pred.edges().addClass('pacto-dependent');
      n.addClass('pacto-hot');
    });
  }

  // Additive only, and always last: a legend filter narrows whatever is already on
  // screen, so it can never resurrect something a pin or an owner view has faded, and
  // never overrides them. Hiding a node kind hides its edges too — an edge to nowhere is
  // a line the reader cannot resolve.
  function fadeLegendHidden(): void {
    if (!legendHidden?.size) return;
    for (const key of legendHidden) {
      const sel = LEGEND_SELECTORS[key];
      if (!sel) continue;
      const els = cy.$(sel);
      els.addClass('pacto-faded');
      if (key.startsWith('kind:')) els.connectedEdges().addClass('pacto-faded');
    }
  }

  // Resting emphasis (no hover/pin): owner view dims to the owner's services; the
  // status/name filter dims non-matches; otherwise the calm full map. The legend filter
  // composes on top of all of them.
  function applyDimming(): void {
    cy.batch(() => {
      cy.elements().removeClass(HL);
      if (!clickFocusId) {
        if (ownerSet) {
          const ids = ownerVisible();
          const kn = cy.nodes().filter((n) => ids.has(n.id()));
          const keep = kn.union(kn.connectedEdges());
          cy.elements().not(keep).addClass('pacto-faded');
        } else if (filterHidden) {
          const faded = cy.nodes().filter((n) => filterHidden!.has(n.id()));
          faded.addClass('pacto-faded');
          faded.connectedEdges().addClass('pacto-faded');
        }
      }
    });
    if (clickFocusId) spotlight(cy.getElementById(clickFocusId));
    cy.batch(fadeLegendHidden);
  }

  // ── Interactions ─────────────────────────────────────────────────────────
  // Pin (or unpin) a node's directional spotlight and gently center it. SETS the pin
  // rather than toggling it, and fires no selection callback, so a caller outside the
  // canvas -- the text list picking the same node the drawer is already showing -- can
  // ask for the pin without the canvas answering back into the call that made it.
  function pinFocus(id: string | null): void {
    clickFocusId = id;
    applyDimming();
    if (!id) return;
    const n = cy.getElementById(id);
    if (n.empty()) return;
    // A pin re-frames; it never re-lays-out. Our own centering, not a pan the user
    // asked for, and on the product's row duration -- 250ms of camera for a click is
    // the reader waiting on the UI to agree with them.
    suppressViewport(MOTION.row + 150);
    if (prefersReducedMotion()) cy.center(n);
    else cy.animate({ center: { eles: n } }, { duration: MOTION.row, easing: 'ease-in-out' });
  }

  let lastTapId = '';
  let lastTapAt = 0;
  cy.on('tap', 'node', (evt) => {
    const n = evt.target as NodeSingular;
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
    // keeping the whole map in view (no hard zoom) so the global picture stays. A second
    // tap on the same node unpins.
    pinFocus(clickFocusId === id ? null : id);
    onSelect?.(clickFocusId ? n.data('serviceName') : null);
    onSelectNode?.(clickFocusId ? id : null);
  });
  // Edge tap: open the relationship's quick-inspection drawer (the product graph
  // reads the backend-authoritative edge; it never navigates away automatically).
  cy.on('tap', 'edge', (evt) => {
    const e = evt.target as EdgeSingular;
    onSelectEdge?.(e.id());
  });
  // An edge is a click target too (it opens the relationship drawer), so it answers the
  // pointer like every other one. Class-only: no batch, no dimming recompute.
  cy.on('mouseover', 'edge', (evt) => {
    container.style.cursor = 'pointer';
    (evt.target as EdgeSingular).addClass('pacto-edge-hot');
  });
  cy.on('mouseout', 'edge', (evt) => {
    container.style.cursor = 'default';
    (evt.target as EdgeSingular).removeClass('pacto-edge-hot');
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
    if (clickFocusId) return;
    cy.batch(() => cy.elements().removeClass(HL));
    spotlight(n);
    // The wipe above clears every fade, including the legend's. Re-apply it, or a hover
    // would silently un-hide the categories the reader just turned off.
    cy.batch(fadeLegendHidden);
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

  // computeDiagnostics reports the read-only readiness of the rendered graph: headless
  // flag, node/edge counts, and how many nodes/edges have real rendered geometry (a nonzero
  // rendered bounding box). It reads only what the canvas already shows, so it is a safe
  // acceptance seam (see GraphDiagnostics), not a leak of internal state.
  function computeDiagnostics(): GraphDiagnostics {
    const base: GraphDiagnostics = { headless: headlessMode, nodeCount: cy.nodes().size(), edgeCount: cy.edges().size(), nodesWithBox: 0, edgesRendered: 0 };
    // A headless instance has no renderer, so rendered geometry is undefined; report zeros
    // rather than calling renderedBoundingBox (which needs a renderer). The browser
    // acceptance requires headless=false anyway.
    if (headlessMode) return base;
    let nodesWithBox = 0;
    cy.nodes().forEach((n) => {
      const bb = n.renderedBoundingBox();
      if ((bb.w ?? 0) > 0 && (bb.h ?? 0) > 0) nodesWithBox++;
    });
    let edgesRendered = 0;
    cy.edges().forEach((e) => {
      const bb = e.renderedBoundingBox();
      if ((bb.w ?? 0) > 0 || (bb.h ?? 0) > 0) edgesRendered++;
    });
    return { ...base, nodesWithBox, edgesRendered };
  }

  // ── Spatial state ────────────────────────────────────────────────────────
  // Where the user put things, and how they are looking at it. It is presentation
  // geometry and nothing else, so a stale or foreign one can only misplace a node --
  // never misreport a status. It is deliberately kept OUT of the semantic refresh path:
  // a background poll that changes a compliance verdict must not move the canvas.

  // Ids we restored a position for. Non-empty means the user has already arranged this
  // exact graph query, so the initial layout is skipped entirely.
  const restored = new Set<string>();
  // True once the arrangement or the viewport belongs to the user (a drag, a pan, a
  // zoom, or a restored state). While it is false the container-resize handler is free
  // to re-fit; once true, re-fitting would throw away a deliberate choice.
  let userAdjusted = false;
  // Viewport events fire for our OWN fits and centering animations too. Programmatic
  // changes announce themselves here so they are not mistaken for user intent.
  let suppressViewportUntil = 0;
  let spatialTimer: ReturnType<typeof setTimeout> | undefined;

  function suppressViewport(ms: number): void { suppressViewportUntil = Date.now() + ms; }

  function snapshotSpatial(): SpatialState {
    const positions: Record<string, { x: number; y: number }> = {};
    cy.nodes().forEach((n) => { positions[n.id()] = { x: n.position('x'), y: n.position('y') }; });
    return { positions, pan: { ...cy.pan() }, zoom: cy.zoom() };
  }

  // Debounced: a drag or a pinch-zoom emits a continuous stream of events, and the
  // caller writes this to storage.
  function emitSpatial(): void {
    if (!onSpatialChange) return;
    clearTimeout(spatialTimer);
    spatialTimer = setTimeout(() => onSpatialChange(snapshotSpatial()), 250);
  }

  function restoreSavedPositions(): void {
    if (!savedPositions) return;
    cy.batch(() => {
      cy.nodes().forEach((n) => {
        const p = savedPositions[n.id()];
        // Ids not in the saved state are ignored rather than defaulted, so a saved
        // state from a smaller graph pre-arranges what it knows and leaves the rest
        // to be placed.
        if (!p || !Number.isFinite(p.x) || !Number.isFinite(p.y)) return;
        n.position({ x: p.x, y: p.y });
        restored.add(n.id());
      });
    });
  }
  restoreSavedPositions();

  // How far a brand-new node sits from the neighbor it attaches to.
  const NEW_NODE_OFFSET = 150;

  // placeNewNodes gives nodes that have no position yet a deterministic spot beside the
  // neighbors they connect to, WITHOUT touching anything already placed. Running a full
  // layout instead would be less code and would also move every surviving node -- which
  // is the exact regression this exists to prevent.
  function placeNewNodes(fresh: Set<string>): void {
    if (!fresh.size) return;
    const settled = cy.nodes().filter((n) => !fresh.has(n.id()));
    const bb = settled.nonempty() ? settled.boundingBox() : null;
    const center = bb ? { x: bb.x1 + bb.w / 2, y: bb.y1 + bb.h / 2 } : { x: 0, y: 0 };
    const radius = bb ? Math.max(200, bb.w / 2 + 140) : 200;
    const ids = [...fresh].sort(); // stable: the same new set always lands the same way
    cy.batch(() => {
      ids.forEach((id, i) => {
        const n = cy.getElementById(id);
        if (n.empty()) return;
        const angle = (i * 2 * Math.PI) / ids.length;
        const anchors = n.neighborhood('node').filter((m) => !fresh.has(m.id()));
        if (anchors.nonempty()) {
          let sx = 0;
          let sy = 0;
          anchors.forEach((m) => { sx += m.position('x'); sy += m.position('y'); });
          const ax = sx / anchors.size();
          const ay = sy / anchors.size();
          n.position({ x: ax + Math.cos(angle) * NEW_NODE_OFFSET, y: ay + Math.sin(angle) * NEW_NODE_OFFSET });
        } else {
          // Nothing to attach to: park it on a ring outside the settled graph, where it
          // is visible as an arrival rather than buried under the existing arrangement.
          n.position({ x: center.x + Math.cos(angle) * radius, y: center.y + Math.sin(angle) * radius });
        }
      });
    });
  }

  // A fit is allowed to zoom OUT until the whole graph is in frame, and on a wide fleet
  // or a phone that lands somewhere around 0.15 -- node labels stop being text and the
  // canvas becomes a picture of a graph rather than a graph. Below this the reader is
  // better served by a legible graph they have to pan than an illegible one they do not.
  const ZOOM_FLOOR = 0.6;

  function floorZoom(): void {
    if (cy.zoom() >= ZOOM_FLOOR) return;
    cy.zoom({ level: ZOOM_FLOOR, renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } });
  }

  // fitView fits the whole graph in view, honoring prefers-reduced-motion (an instant
  // fit rather than an animated pan/zoom).
  function fitView(duration: number): void {
    suppressViewport(duration + 150);
    if (prefersReducedMotion() || duration <= 0) { cy.fit(cy.elements(), 30); floorZoom(); return; }
    cy.animate({ fit: { eles: cy.elements(), padding: 30 } }, { duration, easing: 'ease-out', complete: floorZoom });
  }

  function settle(): void {
    if (filterFn) applyFilter(filterFn);
    else applyDimming();
    // The graph has laid out and painted: publish the readiness snapshot for the
    // visual-graph browser acceptance (a real non-headless canvas with rendered nodes
    // and edges), and never before positions exist.
    onReady?.(computeDiagnostics());
  }

  function runLayout(): void {
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
      settle();
      emitSpatial();
    });
    // fCoSE fits as part of its own run (`fit: true`), and that fit fires `viewport`
    // like any other. Unsuppressed, the FIRST layout of every graph marked the viewport
    // user-adjusted before the user had touched anything -- after which the resize
    // handler politely refused to re-fit for the rest of the session.
    suppressViewport(LAYOUT_SETTLE_MS);
    l.run();
  }

  function layoutAndFit(): void {
    if (restored.size === 0) { runLayout(); return; }
    // The user already arranged this exact graph query. Laying it out again would
    // discard that, so only the nodes the saved state did not know about are placed,
    // and the saved viewport (if any) is restored rather than re-fitted.
    const fresh = new Set<string>();
    cy.nodes().forEach((n) => { if (!restored.has(n.id())) fresh.add(n.id()); });
    placeNewNodes(fresh);
    if (savedViewport && Number.isFinite(savedViewport.zoom) && savedViewport.zoom > 0) {
      suppressViewport(150);
      cy.zoom(savedViewport.zoom);
      cy.pan({ ...savedViewport.pan });
      userAdjusted = true;
    } else {
      fitView(0);
    }
    settle();
  }

  cy.on('dragfree', 'node', () => { userAdjusted = true; emitSpatial(); });
  cy.on('viewport', () => {
    if (Date.now() < suppressViewportUntil) return;
    userAdjusted = true;
    emitSpatial();
  });

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
        // A panel opening must not undo a pan the user just made. Re-fitting is only
        // ever a courtesy for a viewport nobody has touched.
        if (userAdjusted) return;
        if (layout === 'layered') runLayout();
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

  // applyTopology reconciles a CHANGED topology in place. Everything that survives keeps
  // the exact position it had; only elements that genuinely appeared are added (and
  // placed near their neighbors), and only elements that genuinely vanished are removed.
  // The viewport is untouched. Rebuilding the instance instead would be far less code and
  // would relayout the whole graph, which is what made a background refresh feel like a
  // different screen.
  function applyTopology(next: GraphData): void {
    const els = buildElements(next, focusId);
    const keep = new Set(els.map((e) => String(e.data.id)));
    const before = new Set<string>();
    cy.nodes().forEach((n) => { before.add(n.id()); });
    cy.batch(() => {
      cy.elements().filter((e) => !keep.has(e.id())).remove();
      const existing = new Set<string>();
      cy.elements().forEach((e) => { existing.add(e.id()); });
      const added = els.filter((e) => !existing.has(String(e.data.id)));
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      if (added.length) cy.add(added as any);
    });
    // Semantic fields of the survivors still need updating: a topology change and a
    // status change routinely arrive in the same refresh.
    patchData(next);
    const fresh = new Set<string>();
    cy.nodes().forEach((n) => { if (!before.has(n.id())) fresh.add(n.id()); });
    placeNewNodes(fresh);
    fresh.forEach((id) => restored.add(id)); // now arranged; a later refresh must keep it
    if (filterFn) applyFilter(filterFn);
    else applyDimming();
    emitSpatial();
  }

  // resetLayout is the explicit escape hatch: discard the current arrangement and lay the
  // graph out again from scratch. It is NOT fit() -- fit re-frames what is already
  // arranged, this rearranges. The grid seed matters: fCoSE is seeded from the current
  // positions (randomize: false), so without it "reset" would start from the very
  // arrangement it is meant to discard.
  function resetLayout(): void {
    restored.clear();
    userAdjusted = false;
    const ids: string[] = [];
    cy.nodes().forEach((n) => { ids.push(n.id()); });
    ids.sort();
    const cols = Math.max(1, Math.ceil(Math.sqrt(ids.length)));
    cy.batch(() => ids.forEach((id, i) => {
      cy.getElementById(id).position({ x: (i % cols) * 180, y: Math.floor(i / cols) * 140 });
    }));
    runLayout();
  }

  return {
    nodes,
    destroy: () => { ro?.disconnect(); clearTimeout(resizeTimer); clearTimeout(spatialTimer); cy.destroy(); },
    zoomIn: () => zoomBy(1.4),
    zoomOut: () => zoomBy(0.7),
    resetView: () => { clickFocusId = null; applyDimming(); fitView(300); },
    fit: () => fitView(250),
    patchData,
    applyTopology,
    restyle: () => { cy.style(cyStylesheet(resolvePalette(container), layout, edgeStyle)); },
    resetLayout,
    spatialState: snapshotSpatial,
    applyFilter,
    applyLegendFilter: (hidden) => { legendHidden = hidden?.size ? hidden : null; applyDimming(); },
    focusNode: pinFocus,
    diagnostics: computeDiagnostics,
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
