<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl, ownersUrl } from '../lib/router.ts';
  import { ownerKey, extractOwnerDetail } from '../lib/format.ts';
  import SummaryBar from '../components/SummaryBar.svelte';
  import ServicesTable from '../components/ServicesTable.svelte';
  import GraphCanvas from '../GraphCanvas.svelte';

  let { owner = '', services = [], initialLoading = false } = $props();

  let graphData = $state(null);
  let graphLoading = $state(true);

  // Services belonging to this owner
  let ownerServices = $derived(
    services.filter((s) => (ownerKey(s.owner) || '(unowned)') === owner)
  );

  // Structured owner detail extracted from services
  let ownerDetail = $derived(extractOwnerDetail(owner, ownerServices));

  // Set of service names for graph focusing (passed to GraphCanvas as focusNodes)
  let ownerServiceNames = $derived(new Set(ownerServices.map((s) => s.name)));

  onMount(async () => {
    try {
      graphData = await api.graph();
    } catch {}
    graphLoading = false;
  });
</script>

<!-- Breadcrumb -->
<nav class="breadcrumb fade-in" aria-label="Breadcrumb">
  <a href="#/">Services</a>
  <span class="sep">/</span>
  <a href={ownersUrl()}>Owners</a>
  <span class="sep">/</span>
  <span>{owner}</span>
</nav>

<header class="detail-header fade-in-up">
  <h1>{owner}</h1>
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
    <div class="table-wrap fade-in-up">
      <ServicesTable services={ownerServices} />
    </div>
  </div>
{:else}
  <div class="state-box">
    {#if initialLoading}
      <div class="skeleton-table fade-in">
        {#each Array(3) as _}
          <div class="skeleton-row">
            <div class="skeleton skeleton-line" style="width:30%"></div>
            <div class="skeleton skeleton-line" style="width:15%"></div>
            <div class="skeleton skeleton-line" style="width:20%"></div>
          </div>
        {/each}
      </div>
      <p style="margin-top:var(--sp-3); color:var(--c-text-3)">Loading services…</p>
    {:else}
      <h3>No services</h3>
      <p>No services found for owner "{owner}".</p>
    {/if}
  </div>
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
    {:else if graphData?.nodes?.length > 0}
      <p class="text-3" style="font-size:var(--text-xs); margin-bottom:var(--sp-3)">Services owned by {owner} are highlighted; others are dimmed.</p>
      <div class="graph-wrap">
        <GraphCanvas
          {graphData}
          focusNodes={ownerServiceNames}
          height={Math.min(window.innerHeight - 300, 500)}
          onNavigate={(name) => location.hash = serviceUrl(name)}
        />
      </div>
    {:else}
      <p class="text-3" style="font-size:var(--text-xs)">No dependency data available.</p>
    {/if}
  </div>
{/if}

<style>
  .breadcrumb {
    font-size: var(--text-sm); margin-bottom: var(--sp-4);
    color: var(--c-text-3); display: flex; align-items: center; gap: 6px;
  }
  .breadcrumb a { color: var(--c-text-3); }
  .breadcrumb a:hover { color: var(--c-text); }
  .sep { color: var(--c-text-3); }

  .detail-header {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-5); flex-wrap: wrap;
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

  .skeleton-table { width: 100%; max-width: 600px; }
  .skeleton-row { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-3); }
  .skeleton-row .skeleton-line { height: 18px; border-radius: var(--radius-xs); }
</style>
