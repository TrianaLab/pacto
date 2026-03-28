<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { phaseClass } from '../lib/format.ts';
  import GraphCanvas from '../GraphCanvas.svelte';
  import StatsBar from '../StatsBar.svelte';

  let { services = [], sourcesInfo = [] } = $props();

  let graphData = $state(null);
  let loading = $state(true);
  let graphRef = $state(null);
  let phaseFilter = $state('all');
  let nameFilter = $state('');

  async function loadGraph() {
    loading = true;
    try {
      graphData = await api.graph();
    } catch {}
    loading = false;
  }

  function filterFn(node) {
    let dominated = false;
    if (phaseFilter !== 'all') {
      const phase = node.status === 'external' ? 'external' : node.status;
      if (phase !== phaseFilter) dominated = true;
    }
    if (nameFilter) {
      const q = nameFilter.toLowerCase();
      if (!node.serviceName.toLowerCase().includes(q)) dominated = true;
    }
    return dominated;
  }

  $effect(() => {
    if (graphRef) graphRef.applyFilter((phaseFilter === 'all' && !nameFilter) ? null : filterFn);
  });

  onMount(() => { loadGraph(); });
</script>

<div class="graph-header">
  <a href="#/" class="btn btn-sm btn-ghost">← Services</a>
  <h1>Dependency Graph</h1>
</div>

<StatsBar {services} bind:phaseFilter bind:nameFilter />

{#if loading}
  <div class="fade-in" style="padding:var(--sp-4) 0">
    <div class="skeleton" style="width:100%; height:400px; border-radius:var(--radius-sm)"></div>
  </div>
{:else if !graphData?.nodes?.length}
  <div class="state-box"><h3>No services to graph</h3><p>Services need dependencies to appear in the graph.</p></div>
{:else}
  <div class="graph-page-canvas fade-in-up">
    <div class="graph-controls">
      <button type="button" class="btn btn-sm" onclick={() => graphRef?.zoomIn()} title="Zoom in">+</button>
      <button type="button" class="btn btn-sm" onclick={() => graphRef?.zoomOut()} title="Zoom out">−</button>
      <button type="button" class="btn btn-sm" onclick={() => graphRef?.resetView()} title="Reset">↻</button>
    </div>
    <GraphCanvas
      bind:this={graphRef}
      {graphData}
      height={500}
      onNavigate={(name) => location.hash = serviceUrl(name).slice(0)}
    />
    <div class="graph-legend">
      <span class="legend-item"><span class="legend-dot" style="background:var(--c-ok)"></span> Healthy</span>
      <span class="legend-item"><span class="legend-dot" style="background:var(--c-warn)"></span> Degraded</span>
      <span class="legend-item"><span class="legend-dot" style="background:var(--c-err)"></span> Invalid</span>
      <span class="legend-item"><span class="legend-dot" style="background:var(--c-neutral)"></span> Unknown</span>
      <span class="legend-item"><span class="legend-dot" style="background:var(--c-text-3)"></span> external</span>
      <span class="legend-sep">|</span>
      <span class="legend-item"><span class="legend-line solid"></span> required</span>
      <span class="legend-item"><span class="legend-line dashed"></span> optional</span>
      <span class="legend-item"><span class="legend-line ref"></span> reference</span>
    </div>
  </div>

  <!-- Connections table -->
  {@const filteredNodes = graphData.nodes.filter((n) => {
      if (phaseFilter !== 'all') {
        const phase = n.status === 'external' ? 'external' : n.status;
        if (phase !== phaseFilter) return false;
      }
      if (nameFilter) {
        if (!n.serviceName.toLowerCase().includes(nameFilter.toLowerCase())) return false;
      }
      return true;
    })
  }
  {#if filteredNodes.length > 0}
    <div class="section" style="margin-top:var(--sp-6)">
      <div class="section-title">Service Connections <span class="tab-count">{filteredNodes.length}</span></div>
      <div class="table-wrap">
        <table>
          <thead><tr><th data-tip="Service name">Service</th><th data-tip="Service health phase">Status</th><th data-tip="Services this one depends on">Dependencies</th></tr></thead>
          <tbody>
            {#each filteredNodes as node}
              {@const edges = node.edges || []}
              <tr class={node.status !== 'external' ? 'clickable' : ''} onclick={() => { if (node.status !== 'external') location.hash = serviceUrl(node.serviceName).slice(0); }}>
                <td>
                  {#if node.status !== 'external'}
                    <a href={serviceUrl(node.serviceName)}>{node.serviceName}</a>
                  {:else}
                    {node.serviceName} <span class="badge badge-neutral">external</span>
                  {/if}
                </td>
                <td><span class="badge badge-{phaseClass(node.status === 'external' ? 'Unknown' : node.status)}"><span class="badge-dot"></span>{node.status}</span></td>
                <td>
                  {#if edges.length > 0}
                    {#each edges as e, j}
                      <a href={serviceUrl(e.targetName)} onclick={(ev) => ev.stopPropagation()}>{e.targetName}</a>
                      {#if e.type === 'reference'} <span class="badge badge-accent" style="font-size:10px">ref</span>{:else if e.required} <span class="badge badge-info" style="font-size:10px">req</span>{/if}
                      {#if j < edges.length - 1}, {/if}
                    {/each}
                  {:else}
                    <span class="text-dim">—</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
{/if}

<style>
  .graph-header {
    display: flex; align-items: center; gap: var(--sp-3); margin-bottom: var(--sp-5); flex-wrap: wrap;
  }
  .graph-page-canvas { position: relative; }
  .graph-controls {
    position: absolute; top: 12px; right: 12px; z-index: 10;
    display: flex; gap: 4px;
  }

  .graph-legend {
    display: flex; align-items: center; gap: 12px; flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3);
    font-size: var(--text-xs); color: var(--c-text-3);
  }
  .legend-item { display: flex; align-items: center; gap: 4px; }
  .legend-dot { width: 8px; height: 8px; border-radius: 50%; }
  .legend-sep { color: var(--c-border); }
  .legend-line { display: inline-block; width: 16px; height: 0; }
  .legend-line.solid { border-top: 2px solid var(--c-text-2); }
  .legend-line.dashed { border-top: 1px dashed var(--c-text-3); }
  .legend-line.ref { border-top: 1.5px dashed var(--c-accent); }

  .text-dim { color: var(--c-text-3); }
</style>
