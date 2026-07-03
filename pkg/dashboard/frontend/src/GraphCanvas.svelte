<script>
  import { onMount, untrack } from 'svelte';
  import { renderGraph, extractSubgraph } from './lib/graph.ts';
  import { computeVisible } from './lib/layout.ts';

  let {
    graphData = null, focusId = null, height = 400, onNavigate, filterFn, focusNodes = null,
    layout = 'force', direction = 'down', depth = 2, childCap = 12,
  } = $props();

  let containerEl = $state(null);
  let instance = $state(null);
  let expanded = new Set(); // service ids the user expanded via "+N"
  let renderVersion = $state(0); // bump to force re-render after expand/reset

  function onExpand(id) { expanded.add(id); renderVersion++; }
  // Re-run the layout from scratch: clears manual expansions, re-lays out (dagre
  // in layered mode) and refits the view. Restores the "original view" after the
  // user has dragged nodes or expanded subtrees.
  export function reset() { expanded = new Set(); renderVersion++; }

  function init() {
    if (!containerEl || !graphData) return;
    if (instance) instance.destroy();

    const isLayered = layout === 'layered';
    let data = null;
    let hidden;
    if (isLayered && focusId) {
      const r = computeVisible(graphData, { rootId: focusId, direction, depth, expanded, childCap });
      data = { nodes: r.nodes };
      hidden = r.hidden;
    } else if (focusId) {
      data = extractSubgraph(graphData, focusId);
    } else {
      data = graphData;
    }
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
      layout,
      hidden,
      onExpand,
    });
  }

  onMount(() => {
    return () => { if (instance) instance.destroy(); };
  });

  $effect(() => {
    // Re-render on data/layout/tree-shape changes (not callback props).
    const _ = [graphData, focusId, containerEl, layout, direction, depth, renderVersion];
    if (graphData && containerEl) {
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
