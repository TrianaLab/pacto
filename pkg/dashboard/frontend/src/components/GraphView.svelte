<script>
  import { onMount } from 'svelte';
  import { graphData, services, phaseFilter, enabledSources, navigateTo, isSourceEnabled } from '../lib/stores.js';
  import { getSources, isMonitoredPhase } from '../lib/helpers.js';
  import { renderGraph } from '../lib/graph.js';
  import PhaseBadge from './PhaseBadge.svelte';

  let containerEl = $state(null);
  let graphInstance = $state(null);
  let cachedPositions = {};

  function isNodeFiltered(d) {
    const filter = $phaseFilter;
    const enabled = $enabledSources;
    if (filter !== 'all') {
      const phase = (d.status === 'Unmonitored' || d.status === 'External' || d.status === 'Reference') ? 'Unknown' : d.status;
      if (phase !== filter) return true;
    }
    if (Object.keys(enabled).length > 0) {
      const svc = $services.find((s) => s.name === d.serviceName);
      const nodeSources = svc ? getSources(svc) : (d.source ? [d.source] : []);
      if (!nodeSources.some((s) => isSourceEnabled(s, enabled))) return true;
    }
    return false;
  }

  function initGraph() {
    if (!containerEl || !$graphData?.nodes?.length) return;
    if (graphInstance) graphInstance.destroy();
    graphInstance = renderGraph(containerEl, $graphData, {
      cachedPositions,
      onNavigate: (name) => navigateTo('detail', name),
    });
    graphInstance.applyFilter(isNodeFiltered);
  }

  // Legend data
  let legendCounts = $derived.by(() => {
    if (!graphInstance?.nodes) return {};
    const counts = {};
    for (const n of graphInstance.nodes) {
      if (!isNodeFiltered(n)) {
        counts[n.status] = (counts[n.status] || 0) + 1;
      }
    }
    return counts;
  });

  // Connections table data
  let connections = $derived($graphData?.nodes || []);

  // React to filter changes
  $effect(() => {
    $phaseFilter;
    $enabledSources;
    if (graphInstance) {
      graphInstance.applyFilter(isNodeFiltered);
    }
  });

  onMount(() => {
    initGraph();
    return () => {
      if (graphInstance) graphInstance.destroy();
    };
  });

  // Re-init when graph data changes structurally
  $effect(() => {
    if ($graphData && containerEl) {
      initGraph();
    }
  });

  const statusColors = {
    Healthy: 'var(--ok)', Degraded: 'var(--warning)', Invalid: 'var(--critical)',
    Unmonitored: 'var(--neutral)', External: 'var(--neutral)',
  };
  const legendStatuses = ['Healthy', 'Degraded', 'Invalid', 'Unmonitored', 'External'];
</script>

<div class="graph-fullscreen" bind:this={containerEl}>
  <div class="graph-controls">
    <button class="graph-btn" onclick={() => graphInstance?.zoomIn()} title="Zoom in">+</button>
    <button class="graph-btn" onclick={() => graphInstance?.zoomOut()} title="Zoom out">&minus;</button>
    <button class="graph-btn" onclick={() => graphInstance?.resetView()} title="Reset view">{'\u21BA'}</button>
  </div>
  <div class="graph-legend">
    {#each legendStatuses as s}
      {#if legendCounts[s]}
        <span class="legend-item">
          <span class="legend-dot" style="background:{statusColors[s]}"></span>
          {s} ({legendCounts[s]})
        </span>
      {/if}
    {/each}
    <span class="legend-sep">|</span>
    <span class="legend-item" style="font-size:10px"><span style="display:inline-block;width:16px;border-top:2px solid var(--text-secondary)"></span> required</span>
    <span class="legend-item" style="font-size:10px"><span style="display:inline-block;width:16px;border-top:1px dashed var(--text-dim)"></span> optional</span>
    <span class="legend-item" style="font-size:10px"><span style="display:inline-block;width:16px;border-top:1.5px dashed var(--accent)"></span> reference</span>
  </div>
</div>

<!-- Connections table -->
{#if connections.length}
  <div class="graph-connections" style="margin-top:24px">
    <div class="section-heading">Service Connections</div>
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Service</th><th>Status</th><th>Dependencies</th></tr></thead>
        <tbody>
          {#each connections as node}
            {@const edges = node.edges || []}
            {#if node.status === 'external'}
              <tr>
                <td>{node.serviceName} <span class="badge badge-neutral">external</span></td>
                <td><span class="badge badge-neutral"><span class="badge-dot"></span>External</span></td>
                <td>{#if edges.length}{#each edges as e, j}<span class="dep-link" onclick={(ev) => { ev.stopPropagation(); navigateTo('detail', e.targetName); }}>{e.targetName}</span>{#if e.type === 'reference'} <span class="badge badge-accent" style="font-size:10px">ref</span>{:else if e.required} <span class="badge badge-info" style="font-size:10px">req</span>{/if}{#if j < edges.length - 1}, {/if}{/each}{:else}<span class="text-dim">&mdash;</span>{/if}</td>
              </tr>
            {:else}
              <tr data-click onclick={() => navigateTo('detail', node.serviceName)}>
                <td><a>{node.serviceName}</a></td>
                <td><PhaseBadge phase={node.status} /></td>
                <td>{#if edges.length}{#each edges as e, j}<span class="dep-link" onclick={(ev) => { ev.stopPropagation(); navigateTo('detail', e.targetName); }}>{e.targetName}</span>{#if e.type === 'reference'} <span class="badge badge-accent" style="font-size:10px">ref</span>{:else if e.required} <span class="badge badge-info" style="font-size:10px">req</span>{/if}{#if j < edges.length - 1}, {/if}{/each}{:else}<span class="text-dim">&mdash;</span>{/if}</td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}
