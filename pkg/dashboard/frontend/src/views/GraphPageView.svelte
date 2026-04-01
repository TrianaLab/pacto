<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { statusClass, reasonLabel, reasonTooltip, reasonBadgeClass, isReasonActionable, ownerKey, ownerMatchesFilter } from '../lib/format.ts';
  import GraphCanvas from '../GraphCanvas.svelte';
  import StatsBar from '../StatsBar.svelte';

  let { services = [], sourcesInfo = [] } = $props();

  let graphData = $state(null);
  let loading = $state(true);
  let graphRef = $state(null);
  let statusFilter = $state('all');
  let nameFilter = $state('');

  async function loadGraph() {
    loading = true;
    try {
      graphData = await api.graph();
    } catch {}
    loading = false;
  }

  // Build a lookup: service name → owner (for graph filtering)
  let ownerByService = $derived.by(() => {
    const m = new Map();
    for (const s of services) m.set(s.name, s.owner);
    return m;
  });

  function filterFn(node) {
    let dominated = false;
    if (statusFilter !== 'all') {
      const status = node.status === 'external' ? 'external' : node.status;
      if (status !== statusFilter) dominated = true;
    }
    if (nameFilter) {
      const q = nameFilter.toLowerCase();
      const nameMatch = node.serviceName.toLowerCase().includes(q);
      const svcOwner = ownerByService.get(node.serviceName);
      const ownerMatch = svcOwner ? ownerMatchesFilter(svcOwner, q) : false;
      if (!nameMatch && !ownerMatch) dominated = true;
    }
    return dominated;
  }

  $effect(() => {
    if (graphRef) graphRef.applyFilter((statusFilter === 'all' && !nameFilter) ? null : filterFn);
  });

  onMount(() => { loadGraph(); });
</script>

<div class="graph-header">
  <a href="#/" class="btn btn-sm btn-ghost">← Services</a>
  <h1>Dependency Graph</h1>
</div>

<StatsBar {services} bind:statusFilter bind:nameFilter />

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
      height={Math.min(window.innerHeight - 200, 600)}
      onNavigate={(name) => location.hash = serviceUrl(name)}
    />
    <div class="graph-legend">
      <span class="legend-item" data-tip="All contract checks pass"><span class="legend-dot" style="background:var(--c-ok)"></span> Compliant</span>
      <span class="legend-item" data-tip="Some contract checks fail (warnings or errors)"><span class="legend-dot" style="background:var(--c-warn)"></span> Warning</span>
      <span class="legend-item" data-tip="The contract has validation errors"><span class="legend-dot" style="background:var(--c-err)"></span> Non-Compliant</span>
      <span class="legend-item" data-tip="Contract status could not be determined"><span class="legend-dot" style="background:var(--c-neutral)"></span> Unknown</span>
      <span class="legend-sep">|</span>
      <span class="legend-item" data-tip="Non-OCI dependency — not a contract-backed service"><span class="legend-dot" style="background:var(--c-text-3)"></span> External</span>
      <span class="legend-item" data-tip="Registry authentication failed"><span class="legend-dot" style="background:var(--c-err)"></span> Auth required</span>
      <span class="legend-item" data-tip="OCI repo found but no valid semver tags, or registry unreachable"><span class="legend-dot" style="background:var(--c-warn)"></span> Not found / No versions</span>
      <span class="legend-item" data-tip="Background OCI discovery still running"><span class="legend-dot legend-dot-pulse" style="background:var(--c-accent)"></span> Discovering</span>
      <span class="legend-sep">|</span>
      <span class="legend-item"><span class="legend-line solid"></span> required</span>
      <span class="legend-item"><span class="legend-line dashed"></span> optional</span>
      <span class="legend-item"><span class="legend-line ref"></span> reference</span>
    </div>
  </div>

  <!-- Connections table -->
  {@const filteredNodes = graphData.nodes.filter((n) => {
      if (statusFilter !== 'all') {
        const status = n.status === 'external' ? 'external' : n.status;
        if (status !== statusFilter) return false;
      }
      if (nameFilter) {
        const q = nameFilter.toLowerCase();
        const nameMatch = n.serviceName.toLowerCase().includes(q);
        const svcOwner = ownerByService.get(n.serviceName);
        const ownerMatch = svcOwner ? ownerMatchesFilter(svcOwner, q) : false;
        if (!nameMatch && !ownerMatch) return false;
      }
      return true;
    })
  }
  {#if filteredNodes.length > 0}
    <div class="section" style="margin-top:var(--sp-6)">
      <div class="section-title">Service Connections <span class="tab-count">{filteredNodes.length}</span></div>
      <div class="table-wrap table-wrap-fit">
        <table>
          <thead><tr><th data-tip="Service name">Service</th><th data-tip="Contract compliance status">Status</th><th data-tip="Services this one depends on">Dependencies</th></tr></thead>
          <tbody>
            {#each filteredNodes as node}
              {@const edges = node.edges || []}
              <tr class={node.status !== 'external' ? 'clickable' : ''} onclick={() => { if (node.status !== 'external') location.hash = serviceUrl(node.serviceName); }}>
                <td>
                  {#if node.status !== 'external'}
                    <a href={serviceUrl(node.serviceName)}>{node.serviceName}</a>
                  {:else}
                    {node.serviceName} <span class="badge {reasonBadgeClass(node.reason)}" data-tip={reasonTooltip(node.reason)}>{reasonLabel(node.reason)}</span>
                  {/if}
                </td>
                <td>
                  {#if node.status !== 'external'}
                    <span class="badge badge-{statusClass(node.status)}"><span class="badge-dot"></span>{node.status}</span>
                  {:else}
                    <span class="badge {reasonBadgeClass(node.reason)}">{reasonLabel(node.reason)}</span>
                  {/if}
                </td>
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
    display: flex; gap: 6px;
  }

  .graph-legend {
    display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap;
    padding: var(--sp-3) var(--sp-3);
    font-size: var(--text-xs); color: var(--c-text-3);
  }
  .legend-item { display: flex; align-items: center; gap: 5px; }
  .legend-dot { width: 9px; height: 9px; border-radius: 50%; flex-shrink: 0; }
  .legend-dot-pulse { animation: legend-pulse 1.6s ease-in-out infinite; }
  @keyframes legend-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.3; } }
  .legend-sep { color: var(--c-border); }
  .legend-line { display: inline-block; width: 18px; height: 0; }
  .legend-line.solid { border-top: 2px solid var(--c-text-2); }
  .legend-line.dashed { border-top: 1px dashed var(--c-text-3); }
  .legend-line.ref { border-top: 1.5px dashed var(--c-accent); }

  .text-dim { color: var(--c-text-3); }

  /* Override the global min-width for tables that fit on mobile */
  .table-wrap-fit table { min-width: 0; }

  /* ─── Mobile ─── */
  @media (max-width: 768px) {
    .graph-legend { gap: var(--sp-2); font-size: var(--text-xs); }
    .legend-sep { display: none; }
  }
</style>
