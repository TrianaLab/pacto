<script>
  import { onMount, untrack } from 'svelte';
  import { renderGraph, extractSubgraph } from './lib/graph.ts';
  import { computeVisible } from './lib/layout.ts';

  let {
    graphData = null, focusId = null, height = 400, onNavigate, filterFn, focusNodes = null,
    layout = 'force', direction = 'down', depth = 2, childCap = 12, groups = null,
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
      // 'both' unions the down-cone (dependencies) and up-cone (dependents) around
      // the focus so both questions are answered at once; the toolbar can isolate one.
      if (direction === 'both') {
        const down = computeVisible(graphData, { rootId: focusId, direction: 'down', depth, expanded, childCap });
        const up = computeVisible(graphData, { rootId: focusId, direction: 'up', depth, expanded, childCap });
        const byId = new Map();
        for (const n of [...down.nodes, ...up.nodes]) byId.set(n.id, n);
        data = { nodes: [...byId.values()] };
      } else {
        const r = computeVisible(graphData, { rootId: focusId, direction, depth, expanded, childCap });
        data = { nodes: r.nodes };
        hidden = r.hidden;
      }
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
      groups: groups || undefined,
      hidden,
      onExpand,
    });
  }

  onMount(() => {
    return () => { if (instance) instance.destroy(); };
  });

  // Structure signature: re-init (and re-run the layout) ONLY when the graph's
  // shape actually changes — not on every background poll, which re-creates the
  // services array / groups map with identical content and would otherwise re-lay
  // out the whole graph every 1-2s and throw away the user's view.
  function structureSig() {
    if (!graphData?.nodes) return '';
    const ns = graphData.nodes.map((n) => n.id).sort().join(',');
    const es = graphData.nodes
      .flatMap((n) => (n.edges || []).map((e) => `${n.id}>${e.targetId}:${e.type || ''}`))
      .sort().join(',');
    const gs = groups ? [...groups.entries()].map(([k, v]) => `${k}=${v}`).sort().join(',') : '';
    return [ns, es, gs, focusId, layout, direction, depth, renderVersion].join('||');
  }
  let lastSig = '';

  $effect(() => {
    // Track the inputs so the effect re-runs, but only re-init when the signature
    // (structure) changed — a poll with unchanged content is a no-op.
    const _ = [graphData, focusId, containerEl, layout, direction, depth, renderVersion, groups];
    if (!graphData || !containerEl) return;
    const sig = structureSig();
    if (sig === lastSig) return;
    lastSig = sig;
    untrack(() => init());
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
