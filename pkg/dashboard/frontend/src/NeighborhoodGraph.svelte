<script>
  import { onMount } from 'svelte';
  import { renderGraph } from './lib/graph.ts';
  import { neighborhoodToGraph } from './lib/neighborhoodGraph.ts';

  // A thin mount wrapper around the shared Cytoscape engine (lib/graph.ts) for a
  // bounded product ProductNeighborhood. It reuses renderGraph/buildElements/cyLayout/
  // cyStylesheet unchanged and only adapts the data + wires the product callbacks; it
  // never loads the whole fleet and invents no relationships. The focus node is
  // highlighted but not auto-spotlighted, so every returned edge (dependency solid,
  // runs dashed) reads at rest.
  let {
    neighborhood = null, focusKey = '', height = 460,
    onSelectNode, onSelectEdge, oncontrols,
  } = $props();

  let containerEl = $state(null);
  let instance = null;
  let lastSig = '';

  function sig(gd) {
    const ns = gd.nodes.map((n) => n.id).sort().join(',');
    const es = gd.nodes
      .flatMap((n) => (n.edges || []).map((e) => `${n.id}>${e.targetId}:${e.type}`))
      .sort().join(',');
    return [ns, es, focusKey].join('||');
  }

  function init() {
    if (!containerEl) return;
    const gd = neighborhoodToGraph(neighborhood);
    const s = sig(gd);
    if (s === lastSig && instance) return; // background refresh with identical shape
    lastSig = s;
    if (instance) { instance.destroy(); instance = null; }
    if (!gd.nodes.length) { containerEl.innerHTML = ''; oncontrols?.(null); return; }
    instance = renderGraph(containerEl, gd, {
      layout: 'force',
      focusId: focusKey || undefined,
      edgeStyle: 'visible',
      autoSpotlightFocus: false,
      onSelectNode,
      onSelectEdge,
    });
    oncontrols?.({
      fit: () => instance?.fit(),
      zoomIn: () => instance?.zoomIn(),
      zoomOut: () => instance?.zoomOut(),
      reset: () => instance?.resetView(),
    });
  }

  onMount(() => () => { if (instance) instance.destroy(); });

  $effect(() => {
    const _ = [neighborhood, focusKey, containerEl]; // track re-render inputs
    if (containerEl) init();
  });
</script>

<div
  bind:this={containerEl}
  class="nb-graph"
  style="height:{height}px"
  data-testid="neighborhood-canvas"
  role="application"
  aria-label="Operational graph topology. The same relationships are listed as text below the graph."
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
</style>
