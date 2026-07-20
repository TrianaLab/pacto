<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { statusClass, reasonLabel, reasonTooltip, reasonBadgeClass, isReasonActionable, ownerKey, ownerMatchesFilter } from '../lib/format.ts';
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
  let focusRoot = $state('');

  // Graph nodes don't carry source info; map service name → sources from the fleet
  // list so the K8S/OCI pills can actually filter the graph and connections table.
  let sourceByName = $derived(new Map(services.map((s) => [s.name, s.sources || (s.source ? [s.source] : [])])));
  function nodeMatchesSource(node) {
    if (sourceFilter === 'all') return true;
    const srcs = sourceByName.get(node.serviceName);
    return srcs ? srcs.includes(sourceFilter) : false;
  }

  // Cytoscape lays out and fits the whole fleet (dagre + orthogonal edges), so
  // mid-size fleets render legibly with click-to-focus. This threshold is only a
  // coarse cap: above it the full mesh is too dense to be useful, so fall back to a
  // focused neighborhood (root + N hops, widened via the depth control).
  const LARGE_GRAPH = 100;
  let isLarge = $derived((graphData?.nodes?.length || 0) > LARGE_GRAPH);

  // Contract-backed services (not external refs), most-impactful first, for the
  // focus picker. The default focus is the highest-blast-radius service.
  let focusableServices = $derived(
    (graphData?.nodes || [])
      .filter((n) => n.status !== 'external')
      .map((n) => n.serviceName)
      .sort((a, b) => (blastByName.get(b) || 0) - (blastByName.get(a) || 0)),
  );

  // Default the focus to the most-depended-on service once the graph loads.
  $effect(() => {
    if (isLarge && !focusRoot && focusableServices.length) {
      focusRoot = focusableServices[0];
    }
  });

  function serviceMatchesName(name) {
    if (!nameFilter) return false;
    const q = nameFilter.toLowerCase();
    if (name.toLowerCase().includes(q)) return true;
    const o = ownerByService.get(name);
    return o ? ownerMatchesFilter(o, q) : false;
  }

  // In focus mode a searched service may sit outside the current neighborhood, so
  // it would never render — dimming alone greys the whole view. Instead let the
  // search re-focus the graph on the first matching service.
  let nameMatchedFocus = $derived(
    isLarge && nameFilter ? (focusableServices.find(serviceMatchesName) || '') : '',
  );
  let effectiveFocusId = $derived(isLarge ? (nameMatchedFocus || focusRoot) : null);

  // Graph dimming: status always applies. Name dimming only in the full (small)
  // view — in focus mode the name search re-focuses instead (see above), so
  // dimming by name there would grey out the whole neighborhood.
  function graphFilterFn(node) {
    if (statusFilter !== 'all') {
      const status = node.status === 'external' ? 'external' : node.status;
      if (status !== statusFilter) return true;
    }
    if (!nodeMatchesSource(node)) return true;
    if (!isLarge && nameFilter && !serviceMatchesName(node.serviceName)) return true;
    return false;
  }
  let activeGraphFilterFn = $derived(
    (statusFilter === 'all' && sourceFilter === 'all' && (isLarge || !nameFilter)) ? undefined : graphFilterFn,
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
  {#if isLarge}
    <div class="graph-focus-bar">
      <span class="focus-hint">Large graph — showing what depends on</span>
      <select bind:value={focusRoot} aria-label="Focus service" disabled={!!nameMatchedFocus}>
        {#each focusableServices as name}
          <option value={name}>{name}</option>
        {/each}
      </select>
      <span class="focus-hint">
        {#if nameMatchedFocus}Focused on search match "{nameMatchedFocus}".{:else}Search to refocus, use depth to widen or "+N" to expand a branch.{/if}
      </span>
    </div>
  {/if}

  <div class="fade-in-up">
    <!-- Full fleet: compact fCoSE (fills the canvas, readable). Large-fleet focus
         mode shows a small rooted neighborhood, where a dagre tree reads best. -->
    <GraphPanel
      {graphData}
      layout={isLarge ? 'layered' : 'force'}
      focusId={effectiveFocusId}
      showDirectionDepth={isLarge}
      initialDirection={isLarge ? 'up' : 'down'}
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
  .graph-focus-bar select {
    padding: 4px 8px; border: 1px solid var(--c-border); border-radius: var(--radius-xs);
    background: var(--c-surface); color: var(--c-text); font-size: var(--text-sm);
    max-width: 260px;
  }

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
