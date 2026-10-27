<script>
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl, ownerUrl } from '../lib/router.ts';
  import { statusClass, reasonLabel, reasonTooltip, reasonBadgeClass, isReasonActionable, ownerMatchesFilter, ownerKey, ownerKeyLabel, ownerKeyKind, UNOWNED_KEY, aggregateGraphByOwner, relatedSubgraph, formatCount } from '../lib/format.ts';
  import GraphPanel from '../GraphPanel.svelte';
  import StatsBar from '../StatsBar.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import HelpTip from '../components/HelpTip.svelte';
  import NodeDrawer from '../components/NodeDrawer.svelte';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import { getFilters, setFilter } from '../lib/filters.svelte.ts';
  import { nodeDrawerData } from '../lib/nodeDrawer.ts';
  import { graphBreadcrumbs } from '../lib/breadcrumbs.ts';
  import Pager from '../components/Pager.svelte';
  import CappedList from '../components/CappedList.svelte';
  import SearchPick from '../components/SearchPick.svelte';
  import { clientPage } from '../lib/paging.ts';
  import { listState } from '../lib/hashQuery.svelte.ts';

  // Past this many nodes a whole-fleet layout takes a minute and then hangs the tab
  // (60s at 600 services, a crash at 1,500), and nobody can read it anyway. Above it
  // the page asks for a focus or a grouping instead of laying out everything.
  const MAX_LAYOUT_NODES = 150;

  let { services = [], sourcesInfo = [] } = $props();
  let blastByName = $derived(new Map(services.map(s => [s.name, s.blastRadius || 0])));

  let graphData = $state(null);
  let loading = $state(true);
  let graphError = $state(false);

  let selectedNode = $state(null);
  let drawerData = $derived(nodeDrawerData(selectedNode, services, graphData));

  let f = $derived(getFilters()); // reactive shared filter store
  let crumbs = $derived(graphBreadcrumbs({ group: f.group, focus: f.focus }));

  // StatsBar mutates its bindables directly and uses the 'all' sentinel; the store
  // uses '' for empty. Mirror the store into three writable locals for StatsBar and
  // bridge both directions with an equality guard (setting an equal value is a
  // no-op, so the pair converges rather than looping).
  let statusFilter = $state(untrack(() => f.contractStatus || 'all'));
  let sourceFilter = $state(untrack(() => f.source || 'all'));
  let nameFilter = $state(untrack(() => f.search || ''));

  // local (StatsBar) → store/hash. Track ONLY the local: the store side is read via
  // untrack so an external store change (back/forward nav) does not re-run this effect
  // with a stale local value and clobber the navigation back to the old filter.
  $effect(() => { const v = statusFilter === 'all' ? '' : statusFilter; if (untrack(() => f.contractStatus) !== v) setFilter('contractStatus', v); });
  $effect(() => { const v = sourceFilter === 'all' ? '' : sourceFilter; if (untrack(() => f.source) !== v) setFilter('source', v); });
  $effect(() => { if (untrack(() => f.search) !== nameFilter) setFilter('search', nameFilter); });
  // store → local (back/forward, palette navigation). Track ONLY the store: the local is
  // read via untrack so a user edit does not re-run this effect and revert their input.
  $effect(() => { const v = f.contractStatus || 'all'; if (untrack(() => statusFilter) !== v) statusFilter = v; });
  $effect(() => { const v = f.source || 'all'; if (untrack(() => sourceFilter) !== v) sourceFilter = v; });
  $effect(() => { const v = f.search || ''; if (untrack(() => nameFilter) !== v) nameFilter = v; });

  // Group + focus live straight in the store (not StatsBar bindables).
  let groupByOwner = $derived(f.group === 'owner');
  let focusSel = $derived(f.focus || '');

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
    (groupByOwner || (statusFilter === 'all' && sourceFilter === 'all' && !nameFilter)) ? undefined : graphFilterFn,
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

  // Grouping is by canonical identity — a team and a person of the same name are
  // two groups — but the group reads as the owner's name, with the namespace only
  // where it is needed to tell two same-named groups apart.
  function ownerLabelOf(node) {
    if (node.status === 'external') return '(external)';
    const key = ownerKey(ownerByService.get(node.serviceName));
    if (!key) return UNOWNED_KEY;
    const kind = ownerKeyKind(key);
    return kind ? `${ownerKeyLabel(key)} (${kind})` : key;
  }

  // The per-owner aggregated tree (teams as nodes). Derived once and reused for
  // both the grouped display AND the Focus picker, so the team list stays stable
  // even after drilling into a team (where displayGraph becomes that team's
  // services, not the teams).
  let aggregatedGraph = $derived(graphData && groupByOwner ? aggregateGraphByOwner(graphData, ownerLabelOf) : null);

  // The graph shown:
  //  - not grouped         → full service tree (Focus drills to a service via focusId)
  //  - grouped, no focus    → per-owner aggregated tree (teams + cross-team edges)
  //  - grouped, team focus  → that team's SERVICES + their related deps/dependents
  let displayGraph = $derived.by(() => {
    if (!graphData) return null;
    if (groupByOwner && focusSel) return relatedSubgraph(graphData, (n) => ownerLabelOf(n) === focusSel);
    if (groupByOwner) return aggregatedGraph;
    return graphData;
  });
  // Emphasize the focused team's own services when drilled in.
  let graphFocusNodes = $derived(
    groupByOwner && focusSel
      ? new Set((graphData?.nodes || []).filter((n) => ownerLabelOf(n) === focusSel).map((n) => n.serviceName))
      : undefined,
  );
  // focusId only subsets the full service tree (not the grouped views).
  let graphFocusId = $derived(!groupByOwner ? (focusSel || undefined) : undefined);
  let tooBig = $derived(!graphFocusId && (displayGraph?.nodes?.length ?? 0) > MAX_LAYOUT_NODES);

  // Focus picker options: teams when aggregated (from the stable aggregated graph,
  // NOT displayGraph — which becomes the drilled-in team's services once focused,
  // dropping the selected team from the list), else services (most-impactful first).
  let focusOptions = $derived(
    groupByOwner
      ? (aggregatedGraph?.nodes || []).map((n) => n.serviceName).sort()
      : (graphData?.nodes || [])
          .filter((n) => n.status !== 'external')
          .map((n) => n.serviceName)
          .sort((a, b) => (blastByName.get(b) || 0) - (blastByName.get(a) || 0)),
  );

  // Switching grouping changes the node set, so a stale focus no longer applies.
  function toggleGroup() {
    setFilter('group', groupByOwner ? '' : 'owner');
    setFilter('focus', '');
    selectedNode = null;
  }

  const connList = $derived(listState(50));

  onMount(() => { loadGraph(); });
</script>

<Breadcrumbs trail={crumbs} />
<div class="graph-header">
  <h1>Dependency Graph</h1>
</div>

<StatsBar {services} bind:statusFilter bind:sourceFilter bind:nameFilter />

{#if loading}
  <div class="fade-in" style="padding:var(--sp-4) 0">
    <div class="skeleton" style="width:100%; height:400px; border-radius:var(--radius-sm)"></div>
  </div>
{:else if graphError}
  <EmptyState error onRetry={loadGraph} title="Couldn’t load the graph" />
{:else if !graphData?.nodes?.length}
  <EmptyState title="No services to graph" message="Services show up here once they declare dependencies." />
{:else}
  <div class="graph-focus-bar">
    <button type="button" class="seg-toggle" class:active={groupByOwner} onclick={toggleGroup} aria-pressed={groupByOwner}>
      {groupByOwner ? 'Grouped by owner' : 'Group by owner'}
    </button>
    <label class="focus-pick">
      <span class="focus-hint">Focus</span>
      <SearchPick id="graph-focus" value={focusSel} options={focusOptions.map((n) => ({ value: n, label: n }))}
        placeholder={groupByOwner ? 'All teams' : 'Whole fleet'} label="Focus the graph on" testid="graph-focus"
        onpick={(v) => { setFilter('focus', v); selectedNode = null; }} />
    </label>
    <!-- How to read and drive the canvas is a definition, one (i) away, not a sentence
         printed above the graph on every visit. -->
    <HelpTip label="this graph" text={`${groupByOwner && focusSel ? `${focusSel}'s services and what they touch.` : groupByOwner ? 'Teams as nodes, edges are cross-team dependencies. Pick a team to see its services.' : 'Roots on top, shared infrastructure at the bottom.'} Hover to light up dependencies (accent) and dependents (amber). Click to pin, double-click to open.`} />
  </div>

  {#if tooBig}
    <div class="graph-too-big" role="status" data-testid="graph-too-big">
      <p>Too many to draw ({displayGraph.nodes.length.toLocaleString('en-US')} {groupByOwner ? 'teams' : 'services'}). Pick a {groupByOwner ? 'team' : 'service'} to focus on{groupByOwner ? '' : ' or group by owner'}. The table below lists them all.</p>
      {#if !groupByOwner}<button type="button" class="btn btn-sm" onclick={toggleGroup}>Group by owner</button>{/if}
    </div>
  {:else}
  <div class="fade-in-up graph-stage">
    <!-- Top-down dependency tree; over-wide levels wrap into sub-rows. Group-by-owner
         aggregates it to a team-level tree; Focus drills to one node's neighborhood. -->
    <GraphPanel
      graphData={displayGraph}
      layout="layered"
      focusId={graphFocusId}
      focusNodes={graphFocusNodes}
      initialDirection="both"
      filterFn={activeGraphFilterFn}
      height={Math.min(window.innerHeight - 200, 640)}
      onNavigate={(name) => location.hash = (groupByOwner && !focusSel) ? ownerUrl(name) : serviceUrl(name)}
      onSelect={(name) => (selectedNode = name)}
      showZoom
      showLegend
    />
    <NodeDrawer data={drawerData} onClose={() => (selectedNode = null)} />
  </div>
  {/if}

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
  {@const connPage = clientPage(filteredNodes, connList.offset, connList.limit)}
  {#if filteredNodes.length > 0}
    <div class="section" style="margin-top:var(--sp-6)">
      <div class="section-title">Service Connections <span class="tab-count">{formatCount(filteredNodes.length)}</span></div>
      <div class="data-scroll">
        <table class="data-table">
          <colgroup><col style="width:30%" /><col style="width:16%" /><col style="width:9%" /><col /></colgroup>
          <thead><tr><th data-tip="Service name">Service</th><th data-tip="Contract compliance status">Status</th><th data-tip="Number of services transitively impacted if this one fails">Blast</th><th data-tip="Services this one depends on">Dependencies</th></tr></thead>
          <tbody>
            {#each connPage.items as node (node.serviceName)}
              {@const edges = node.edges || []}
              {@const blast = blastByName.get(node.serviceName) || 0}
              <tr class={node.status !== 'external' ? 'clickable' : ''} onclick={() => { if (node.status !== 'external') location.hash = serviceUrl(node.serviceName); }}>
                <td data-label="Service">
                  {#if node.status !== 'external'}
                    <a href={serviceUrl(node.serviceName)} class="cell-clip" title={node.serviceName}>{node.serviceName}</a>
                  {:else}
                    {node.serviceName} <span class="badge {reasonBadgeClass(node.reason)}" data-tip={reasonTooltip(node.reason)}>{reasonLabel(node.reason)}</span>
                  {/if}
                </td>
                <td data-label="Status">
                  {#if node.status !== 'external'}
                    <StatusBadge status={node.status} />
                  {:else}
                    <span class="badge {reasonBadgeClass(node.reason)}">{reasonLabel(node.reason)}</span>
                  {/if}
                </td>
                <td data-label="Blast">
                  {#if blast > 0}
                    <span class="blast-badge" class:blast-low={blast < 3} class:blast-med={blast >= 3 && blast < 5} class:blast-high={blast >= 5}>{blast}</span>
                  {:else}
                    <span class="blast-badge blast-zero">0</span>
                  {/if}
                </td>
                <td data-label="Dependencies">
                  {#if edges.length > 0}
                    <!-- One shared capped sentence: the commas are part of each item, so
                         "worker [req] ,payments" can no longer happen, and a service with
                         sixty dependencies shows five and offers the rest. -->
                    <CappedList items={edges} max={5} inline label={`What ${node.serviceName} depends on`}>
                      {#snippet item(e)}<a href={serviceUrl(e.targetName)} onclick={(ev) => ev.stopPropagation()}>{e.targetName}</a>{#if e.type === 'reference'}{' '}<span class="badge badge-accent" style="font-size: var(--text-xs)">ref</span>{:else if e.required}{' '}<span class="badge badge-info" style="font-size: var(--text-xs)">req</span>{/if}{/snippet}
                    </CappedList>
                  {:else}
                    <span class="text-dim">—</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <Pager page={connPage.page} href={connList.href} label="Connection pages" noun="services" testid="connections-pager" />
    </div>
  {/if}
{/if}

<style>
  .graph-stage { position: relative; }
  .graph-header {
    display: flex; align-items: center; gap: var(--sp-3); margin-bottom: var(--sp-5); flex-wrap: wrap;
  }
  .text-dim { color: var(--c-text-3); }

  .graph-focus-bar {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    margin-bottom: var(--sp-2); font-size: var(--text-sm);
  }
  .focus-hint { color: var(--c-text-3); font-size: var(--text-xs); }
  .focus-pick { display: inline-flex; align-items: center; gap: var(--sp-1); }
  .seg-toggle {
    padding: 5px 12px; border-radius: 100px; cursor: pointer;
    border: 1px solid var(--c-border); background: var(--c-surface);
    color: var(--c-text-2); font: inherit; font-size: var(--text-xs); white-space: nowrap;
    min-height: var(--touch-min);
  }
  .seg-toggle:hover { border-color: var(--c-text-3); color: var(--c-text); }
  .seg-toggle.active { border-color: var(--c-accent); background: var(--c-accent-bg); color: var(--c-accent); }

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

  .graph-too-big { padding: var(--sp-4); border: 1px dashed var(--c-border); border-radius: var(--radius-sm); color: var(--c-text-2); }
  .graph-too-big p { margin: 0 0 var(--sp-2); }
</style>
