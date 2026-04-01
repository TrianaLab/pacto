<script>
  import { onMount, untrack } from 'svelte';
  import { renderGraph, extractSubgraph } from './lib/graph.ts';

  let { graphData = null, focusId = null, height = 400, onNavigate, filterFn, focusNodes = null } = $props();

  let containerEl = $state(null);
  let instance = $state(null);

  function init() {
    if (!containerEl || !graphData) return;
    if (instance) instance.destroy();

    const data = focusId ? extractSubgraph(graphData, focusId) : graphData;
    if (!data || !data.nodes?.length) {
      containerEl.innerHTML = '';
      instance = null;
      return;
    }

    instance = renderGraph(containerEl, data, {
      focusId,
      onNavigate,
      filterFn,
      focusNodes: focusNodes || undefined,
    });
  }

  onMount(() => {
    return () => { if (instance) instance.destroy(); };
  });

  $effect(() => {
    // Track only graphData, focusId, containerEl — not callback props
    const _data = graphData;
    const _focus = focusId;
    const _el = containerEl;
    if (_data && _el) {
      untrack(() => init());
    }
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
    touch-action: none; /* Allows D3 zoom/pan to handle touch */
  }
</style>
