<script>
  import { onMount } from 'svelte';
  import { navigateTo } from '../lib/stores.js';
  import { renderGraph, extractSubgraph } from '../lib/graph.js';

  let { graphData = null, focusId = null } = $props();

  let containerEl = $state(null);
  let graphInstance = $state(null);

  function init() {
    if (!containerEl || !graphData) return;
    if (graphInstance) graphInstance.destroy();
    const subgraph = extractSubgraph(graphData, focusId);
    if (!subgraph) {
      containerEl.innerHTML = '<div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:40px">No dependency relationships to display</div>';
      return;
    }
    graphInstance = renderGraph(containerEl, subgraph, {
      focusId,
      onNavigate: (name) => navigateTo('detail', name),
    });
  }

  onMount(() => {
    init();
    return () => {
      if (graphInstance) graphInstance.destroy();
    };
  });

  $effect(() => {
    if (graphData && focusId && containerEl) {
      init();
    }
  });
</script>

<div bind:this={containerEl} style="width:100%;height:400px;position:relative"></div>
