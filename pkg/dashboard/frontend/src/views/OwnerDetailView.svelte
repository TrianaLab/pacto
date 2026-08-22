<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl, ownersUrl } from '../lib/router.ts';
  import { ownerKey, UNOWNED_KEY, ownerKeyLabel, ownerKeyKind, extractOwnerDetail, relatedSubgraph } from '../lib/format.ts';
  import SummaryBar from '../components/SummaryBar.svelte';
  import ServicesTable from '../components/ServicesTable.svelte';
  import GraphPanel from '../GraphPanel.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';

  let { owner = '', services = [], initialLoading = false } = $props();

  let graphData = $state(null);
  let graphLoading = $state(true);
  let graphError = $state(false);

  // Services belonging to this owner
  let ownerServices = $derived(
    services.filter((s) => (ownerKey(s.owner) || UNOWNED_KEY) === owner)
  );

  // `owner` is the canonical key that routed here. The reader gets the name, plus
  // the namespace — without it a team and a person of the same name have two
  // indistinguishable pages.
  let ownerLabel = $derived(owner === UNOWNED_KEY ? owner : ownerKeyLabel(owner));
  let ownerKind = $derived(ownerKeyKind(owner));

  // Structured owner detail extracted from services
  let ownerDetail = $derived(extractOwnerDetail(owner, ownerServices));

  // Set of service names for graph focusing (passed to GraphPanel as focusNodes)
  let ownerServiceNames = $derived(new Set(ownerServices.map((s) => s.name)));

  // Scope the graph to this owner's services + only their related deps/dependents,
  // so unrelated other-team services don't clutter the owner view.
  let ownerGraph = $derived(
    graphData ? relatedSubgraph(graphData, (n) => ownerServiceNames.has(n.serviceName)) : null,
  );

  let crumbs = $derived([{ label: 'Owners', href: ownersUrl() }, { label: ownerLabel }]);

  async function loadOwnerGraph() {
    graphLoading = true;
    graphError = false;
    try {
      graphData = await api.graph();
    } catch {
      graphError = true;
    }
    graphLoading = false;
  }

  onMount(loadOwnerGraph);
</script>

<!-- Breadcrumb -->
<Breadcrumbs trail={crumbs} />

<header class="detail-header fade-in-up">
  <h1>{ownerLabel}</h1>
  {#if ownerKind}<span class="owner-kind-badge">{ownerKind}</span>{/if}
  <span class="text-2">{ownerServices.length} service{ownerServices.length !== 1 ? 's' : ''}</span>
</header>

<!-- Ownership metadata -->
{#if ownerDetail.isStructured}
  <div class="owner-meta fade-in-up">
    <div class="meta-row">
      {#if ownerDetail.team}
        <div class="meta-item">
          <span class="meta-label">Team</span>
          <span class="meta-value">{ownerDetail.team}</span>
        </div>
      {/if}
      {#if ownerDetail.dri}
        <div class="meta-item">
          <span class="meta-label">DRI{ownerDetail.driConflict ? ' (inconsistent)' : ''}</span>
          {#if ownerDetail.driConflict}
            <span class="meta-value dri-conflict">{ownerDetail.allDris.join(', ')}</span>
          {:else}
            <span class="meta-value">{ownerDetail.dri}</span>
          {/if}
        </div>
      {/if}
    </div>
    {#if ownerDetail.contacts.length > 0}
      <div class="meta-contacts">
        <span class="meta-label">Contacts</span>
        <div class="contact-list">
          {#each ownerDetail.contacts as contact}
            <span class="contact-pill">
              <span class="contact-type">{contact.type}</span>
              <span class="contact-value">{contact.value}</span>
              {#if contact.purpose}<span class="contact-purpose">{contact.purpose}</span>{/if}
            </span>
          {/each}
        </div>
      </div>
    {/if}
  </div>
{/if}

<!-- Readiness + compliance summary for this owner's services -->
{#if ownerServices.length > 0}
  <SummaryBar services={ownerServices} />
{/if}

<!-- Services table for this owner -->
{#if ownerServices.length > 0}
  <div class="section">
    <div class="section-title">Services <span class="tab-count">{ownerServices.length}</span></div>
    <!-- ServicesTable provides its own .table-wrap; wrapping it in a second
         .table-wrap nested two overflow contexts and was the source of the
         spurious horizontal scrollbar on the owner-detail view. -->
    <div class="fade-in-up">
      <ServicesTable services={ownerServices} />
    </div>
  </div>
{:else}
  <EmptyState
    loading={initialLoading}
    title={!initialLoading ? 'No services' : undefined}
    message={initialLoading ? 'Loading services…' : `No services found for owner "${owner}".`}
  />
{/if}

<!-- Owner graph -->
{#if ownerServiceNames.size > 0}
  <div class="section" style="margin-top:var(--sp-5)">
    <div class="section-title">Dependency Graph</div>
    {#if graphLoading}
      <div class="fade-in" style="padding:var(--sp-3) 0">
        <div class="skeleton" style="width:100%; height:300px; border-radius:var(--radius-sm)"></div>
        <p class="text-3" style="font-size:var(--text-xs); margin-top:var(--sp-2)">Loading dependency graph…</p>
      </div>
    {:else if ownerGraph?.nodes?.length > 0}
      <p class="text-3" style="font-size:var(--text-xs); margin-bottom:var(--sp-3)">{owner}'s services and the dependencies / dependents they touch. Hover to trace; double-click to open.</p>
      <div class="graph-wrap">
        <GraphPanel
          graphData={ownerGraph}
          focusNodes={ownerServiceNames}
          layout="layered"
          height={Math.min(window.innerHeight - 300, 500)}
          onNavigate={(name) => location.hash = serviceUrl(name)}
          showZoom
          showLegend
          tapToOpen
        />
      </div>
    {:else if graphError}
      <p class="text-3" style="font-size:var(--text-xs)">Couldn’t load the dependency graph. <button type="button" class="link-retry" onclick={loadOwnerGraph}>Retry</button></p>
    {:else}
      <p class="text-3" style="font-size:var(--text-xs)">No dependency data available.</p>
    {/if}
  </div>
{/if}

<style>
  .detail-header {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-5); flex-wrap: wrap;
  }

  .owner-kind-badge {
    font-size: var(--text-xs); color: var(--c-text-2);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    padding: 1px var(--sp-2);
  }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }

  /* ── Owner metadata ── */
  .owner-meta {
    background: var(--c-surface); border: 1px solid var(--c-border);
    border-radius: var(--radius-sm); padding: var(--sp-4);
    margin-bottom: var(--sp-5);
  }
  .meta-row {
    display: flex; gap: var(--sp-5); flex-wrap: wrap;
    margin-bottom: var(--sp-1);
  }
  .meta-item { display: flex; flex-direction: column; gap: 2px; }
  .meta-label {
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3);
  }
  .meta-value { font-size: var(--text-sm); font-weight: 600; color: var(--c-text); }
  .meta-contacts { margin-top: var(--sp-3); }
  .contact-list { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-top: var(--sp-1); }
  .contact-pill {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 4px 10px; border-radius: var(--radius-xs);
    background: var(--c-surface-inset); border: 1px solid var(--c-border);
    font-size: var(--text-xs);
  }
  .contact-type {
    font-weight: 600; text-transform: uppercase;
    color: var(--c-text-3); font-size: 10px; letter-spacing: 0.03em;
  }
  .contact-value { color: var(--c-text); }
  .contact-purpose {
    color: var(--c-text-3); font-style: italic;
  }
  .contact-purpose::before { content: '· '; }
  .dri-conflict { color: var(--c-warn); }

  .graph-wrap { position: relative; }

  .link-retry {
    background: none; border: none; padding: 0;
    color: var(--c-accent); font: inherit; cursor: pointer; text-decoration: underline;
  }
</style>
