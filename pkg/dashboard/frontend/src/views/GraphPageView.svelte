<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { statusClass, reasonLabel, reasonTooltip, reasonBadgeClass, isReasonActionable, ownerMatchesFilter } from '../lib/format.ts';
  import GraphPanel from '../GraphPanel.svelte';
  import StatsBar from '../StatsBar.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  let { services = [], sourcesInfo = [] } = $props();
  let blastByName = $derived(new Map(services.map(s => [s.name, s.blastRadius || 0])));

  let graphData = $state(null);
  let loading = $state(true);
  let graphError = $state(false);
  let statusFilter = $state('all');
  let sourceFilter = $state('all');
  let nameFilter = $state('');

  // Graph nodes don't carry source info; map service name → sources from the fleet
  // list so the K8S/OCI pills can actually filter the graph and connections table.
  let sourceByName = $derived(new Map(services.map((s) => [s.name, s.sources || (s.source ? [s.source] : [])])));
  function nodeMatchesSource(node) {
    if (sourceFilter === 'all') return true;
    const srcs = sourceByName.get(node.serviceName);
    return srcs ? srcs.includes(sourceFilter) : false;
  }

  function serviceMatchesName(name) {
    if (!nameFilter) return false;
    const q = nameFilter.toLowerCase();
    if (name.toLowerCase().includes(q)) return true;
    const o = ownerByService.get(name);
    return o ? ownerMatchesFilter(o, q) : false;
  }

  // Status / source / name filters dim the non-matching nodes on the map.
  function graphFilterFn(node) {
    if (statusFilter !== 'all') {
      const status = node.status === 'external' ? 'external' : node.status;
      if (status !== statusFilter) return true;
    }
    if (!nodeMatchesSource(node)) return true;
    if (nameFilter && !serviceMatchesName(node.serviceName)) return true;
    return false;
  }
  let activeGraphFilterFn = $derived(
    (statusFilter === 'all' && sourceFilter === 'all' && !nameFilter) ? undefined : graphFilterFn,
  );

  async function loadGraph() {
    loading = true;
    graphError = false;
    try {
      graphData = await api.graph();
    } catch {
      graphError = true;
    }
    loading = false;
  }

  // Build a lookup: service name → owner (for graph filtering)
  let ownerByService = $derived.by(() => {
    const m = new Map();
    for (const s of services) m.set(s.name, s.owner);
    return m;
  });

  onMount(() => { loadGraph(); });
</script>

<div class="graph-header">
  <a href="#/" class="btn btn-sm btn-ghost">← Services</a>
  <h1>Dependency Graph</h1>
</div>

<StatsBar {services} bind:statusFilter bind:sourceFilter bind:nameFilter />

{#if loading}
  <div class="fade-in" style="padding:var(--sp-4) 0">
    <div class="skeleton" style="width:100%; height:400px; border-radius:var(--radius-sm)"></div>
  </div>
{:else if graphError}
  <EmptyState error onRetry={loadGraph} title="Couldn’t load the graph" message="The dependency graph couldn’t be fetched." />
{:else if !graphData?.nodes?.length}
  <EmptyState title="No services to graph" message="Services need dependencies to appear in the graph." />
{:else}
  <div class="graph-focus-bar">
    <span class="focus-hint">The whole dependency tree — roots on top, shared infrastructure at the bottom. Hover a service to light up its dependencies (accent →) and dependents / blast radius (amber ←); click to pin, double-click to open.</span>
  </div>

  <div class="fade-in-up">
    <!-- Whole fleet as a top-down dependency tree; over-wide levels wrap into
         sub-rows so one big tier can't stretch it into a band. -->
    <GraphPanel
      {graphData}
      layout="layered"
      filterFn={activeGraphFilterFn}
      height={Math.min(window.innerHeight - 200, 640)}
      onNavigate={(name) => location.hash = serviceUrl(name)}
      showZoom
      showLegend
    />
  </div>

  <!-- Connections table -->
  {@const filteredNodes = graphData.nodes.filter((n) => {
      if (statusFilter !== 'all') {
        const status = n.status === 'external' ? 'external' : n.status;
        if (status !== statusFilter) return false;
      }
      if (!nodeMatchesSource(n)) return false;
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
          <thead><tr><th data-tip="Service name">Service</th><th data-tip="Contract compliance status">Status</th><th data-tip="Number of services transitively impacted if this one fails">Blast</th><th data-tip="Services this one depends on">Dependencies</th></tr></thead>
          <tbody>
            {#each filteredNodes as node}
              {@const edges = node.edges || []}
              {@const blast = blastByName.get(node.serviceName) || 0}
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
                    <StatusBadge status={node.status} />
                  {:else}
                    <span class="badge {reasonBadgeClass(node.reason)}">{reasonLabel(node.reason)}</span>
                  {/if}
                </td>
                <td>
                  {#if blast > 0}
                    <span class="blast-badge" class:blast-low={blast < 3} class:blast-med={blast >= 3 && blast < 5} class:blast-high={blast >= 5}>{blast}</span>
                  {:else}
                    <span class="blast-badge blast-zero">0</span>
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
  .text-dim { color: var(--c-text-3); }

  .graph-focus-bar {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    margin-bottom: var(--sp-2); font-size: var(--text-sm);
  }
  .focus-hint { color: var(--c-text-3); font-size: var(--text-xs); }

  .blast-badge {
    display: inline-flex; align-items: center; justify-content: center;
    min-width: 26px; height: 22px; padding: 0 7px;
    border-radius: var(--radius-xs);
    font-size: var(--text-xs); font-weight: 600;
  }
  .blast-low { background: var(--c-warn-bg); color: var(--c-warn); }
  .blast-med { background: var(--c-warn-bg); color: var(--c-warn); border: 1px solid color-mix(in srgb, var(--c-warn) 25%, transparent); }
  .blast-high { background: var(--c-err-bg); color: var(--c-err); border: 1px solid color-mix(in srgb, var(--c-err) 25%, transparent); }
  .blast-zero { background: var(--c-neutral-bg); color: var(--c-text-3); }

  /* Override the global min-width for tables that fit on mobile */
  .table-wrap-fit table { min-width: 0; }
</style>
