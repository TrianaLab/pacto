<script>
  import { onMount } from 'svelte';
  import { renderGraph, extractSubgraph } from './lib/graph.js';

  let { graphData = null, focusId = null, height = 400, onNavigate, filterFn } = $props();

  let containerEl = $state(null);
  let instance = $state(null);

  function init() {
    if (!containerEl || !graphData) return;
    if (instance) instance.destroy();

    const data = focusId ? extractSubgraph(graphData, focusId) : graphData;
    if (!data || !data.nodes?.length) {
      containerEl.innerHTML = '';
      return;
    }

    instance = renderGraph(containerEl, data, {
      focusId,
      onNavigate,
      filterFn,
    });
  }

  onMount(() => {
    init();
    return () => { if (instance) instance.destroy(); };
  });

  $effect(() => {
    // Re-init when data changes
    if (graphData && containerEl) init();
  });

  export function zoomIn() { instance?.zoomIn(); }
  export function zoomOut() { instance?.zoomOut(); }
  export function resetView() { instance?.resetView(); }
  export function applyFilter(fn) { instance?.applyFilter(fn); }
</script>

<div
  bind:this={containerEl}
  class="graph-container"
  style="height:{height}px"
></div>

<style>
  .graph-container {
    width: 100%;
    position: relative;
    background: var(--c-surface-inset);
    border-radius: var(--radius-sm);
  }
</style>
