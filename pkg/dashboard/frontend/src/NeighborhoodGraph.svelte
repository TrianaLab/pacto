<script>
  import { onMount } from 'svelte';
  import { renderGraph, GraphRenderError } from './lib/graph.ts';
  import { neighborhoodToGraph, topoSignature, presentationSignature } from './lib/neighborhoodGraph.ts';
  import { loadSpatial, saveSpatial, clearSpatial } from './lib/graphSpatial.ts';

  // A thin mount wrapper around the shared Cytoscape engine (lib/graph.ts) for a
  // bounded product ProductNeighborhood. It reuses renderGraph/buildElements/cyLayout/
  // cyStylesheet unchanged and only adapts the data + wires the product callbacks; it
  // never loads the whole fleet and invents no relationships. The focus node is
  // highlighted but not auto-spotlighted, so every returned edge (dependency solid,
  // runs dashed) reads at rest.
  //
  // `queryKey` is the canonical graph-QUERY identity (see lib/graphSpatial.ts). It, not
  // the data, decides when the instance is rebuilt: the same question refreshing is
  // reconciled in place so the arrangement survives, and a DIFFERENT question gets a new
  // instance and its own saved arrangement -- spatial state never leaks between queries.
  let {
    neighborhood = null, focusKey = '', queryKey = '', height = 460,
    onSelectNode, onSelectEdge, oncontrols,
  } = $props();

  let containerEl = $state(null);
  let instance = null;
  // renderError is set when the VISUAL renderer fails in a real browser (a typed
  // GraphRenderError). We then show an explicit render-error state instead of a silently
  // empty canvas; the parent (GraphView) keeps the Relationships text list available.
  let renderError = $state(false);
  // Two signatures so a refresh does the RIGHT thing: the topology
  // signature (node ids + edge endpoints/type + focus) decides reconcile-vs-restyle; the
  // presentation signature (node status/label/kind + edge state) decides whether an
  // in-place restyle is needed. Keying only on topology left the canvas stale when a
  // node flipped Compliant -> NonCompliant or an edge difference changed.
  let lastTopo = '';
  let lastPres = '';
  let lastQuery = null;

  function init() {
    if (!containerEl) return;
    const gd = neighborhoodToGraph(neighborhood);
    const topo = `${topoSignature(gd)}||${focusKey}`;
    const pres = presentationSignature(gd);

    if (instance && queryKey === lastQuery && gd.nodes.length) {
      if (topo !== lastTopo) {
        // Same question, changed answer: reconcile in place. Surviving nodes keep the
        // exact position the user gave them, arrivals are placed beside their neighbors
        // and departures are removed. Rebuilding here is what used to make an ordinary
        // background refresh feel like landing on a different screen.
        lastTopo = topo;
        lastPres = pres;
        instance.applyTopology(gd);
        publishDiagnostics(instance.diagnostics());
        return;
      }
      // Identical topology: only restyle if the semantics actually changed. Positions,
      // pan, zoom and selection are untouched either way.
      if (pres !== lastPres) { instance.patchData(gd); lastPres = pres; }
      return;
    }

    lastTopo = topo;
    lastPres = pres;
    lastQuery = queryKey;
    if (instance) { instance.destroy(); instance = null; }
    if (!gd.nodes.length) { containerEl.innerHTML = ''; oncontrols?.(null); return; }
    // A previously-saved arrangement for THIS question, if the user made one. Anything
    // it cannot vouch for reads as absent, so a stale entry just means a fresh layout.
    const saved = queryKey ? loadSpatial(queryKey) : null;
    try {
      instance = renderGraph(containerEl, gd, {
        layout: 'force',
        focusId: focusKey || undefined,
        edgeStyle: 'visible',
        autoSpotlightFocus: false,
        onSelectNode,
        onSelectEdge,
        onReady: publishDiagnostics,
        savedPositions: saved?.positions,
        savedViewport: saved ? { pan: saved.pan, zoom: saved.zoom } : null,
        onSpatialChange: queryKey ? (s) => saveSpatial(queryKey, s) : undefined,
      });
    } catch (e) {
      // A visual-renderer failure is surfaced honestly; a non-render error is a real bug
      // and must not be swallowed.
      if (e instanceof GraphRenderError) {
        renderError = true;
        instance = null;
        oncontrols?.(null);
        return;
      }
      throw e;
    }
    renderError = false;
    publishSpatialSeam();
    oncontrols?.({
      fit: () => instance?.fit(),
      zoomIn: () => instance?.zoomIn(),
      zoomOut: () => instance?.zoomOut(),
      reset: () => instance?.resetView(),
      // Reset layout is NOT Fit. Fit re-frames the arrangement you have; this discards
      // it -- including the saved one, immediately, so a reload cannot resurrect it --
      // and lays the graph out again from scratch.
      resetLayout: () => {
        if (queryKey) clearSpatial(queryKey);
        instance?.resetLayout();
      },
    });
  }

  // publishDiagnostics mirrors the renderer's readiness snapshot onto stable data
  // attributes, the seam the visual-graph browser acceptance reads to prove a non-headless
  // canvas actually painted nodes and edges (never a mounted-but-empty container).
  function publishDiagnostics(d) {
    if (!containerEl) return;
    containerEl.setAttribute('data-graph-headless', String(d.headless));
    containerEl.setAttribute('data-graph-nodes', String(d.nodeCount));
    containerEl.setAttribute('data-graph-edges', String(d.edgeCount));
    containerEl.setAttribute('data-graph-node-boxes', String(d.nodesWithBox));
    containerEl.setAttribute('data-graph-edges-rendered', String(d.edgesRendered));
    containerEl.setAttribute('data-graph-ready', d.headless ? 'headless' : 'painted');
  }

  // publishSpatialSeam exposes the LIVE Cytoscape geometry to the browser acceptance, the
  // same way publishDiagnostics exposes the paint state. Spatial stability has to be
  // asserted on real positions and a real viewport -- a screenshot cannot tell "the
  // layout was preserved" from "the layout was recomputed and happened to look similar".
  function publishSpatialSeam() {
    if (!containerEl) return;
    containerEl.pactoGraphSpatial = () => (instance ? instance.spatialState() : null);
  }

  onMount(() => () => { if (instance) instance.destroy(); });

  $effect(() => {
    const _ = [neighborhood, focusKey, queryKey, containerEl]; // track re-render inputs
    if (containerEl) init();
  });
</script>

<!-- The canvas is a VISUAL representation (mouse/touch); it is not a complete keyboard
     application, so it is described as an image rather than declaring role="application". The keyboard/screen-reader model is the first-class semantic graph
     navigator rendered as text alongside it (GraphView's Relationships list). -->
{#if renderError}
  <div class="nb-graph-error" role="alert" data-testid="graph-render-error">
    The visual graph could not be rendered. The relationship list below remains available.
  </div>
{/if}
<div
  bind:this={containerEl}
  class="nb-graph"
  style="height:{height}px"
  data-testid="neighborhood-canvas"
  role="img"
  aria-label="Operational graph topology (visual). Use the Relationships list below to explore the same nodes and connections by keyboard."
  hidden={renderError}
></div>

<style>
  .nb-graph {
    width: 100%;
    position: relative;
    background: var(--c-surface-inset);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    touch-action: none;
  }
  .nb-graph-error {
    padding: var(--sp-3);
    border-radius: var(--radius-sm);
    background: var(--c-err-bg);
    border: 1px solid var(--c-err);
    color: var(--c-text);
    font-size: var(--text-sm);
  }
</style>
