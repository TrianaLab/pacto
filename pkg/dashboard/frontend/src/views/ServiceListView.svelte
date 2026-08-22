<script>
  import { serviceUrl } from '../lib/router.ts';
  import { getFilters } from '../lib/filters.svelte.ts';
  import { applyFilters } from '../lib/filters.ts';
  import { treemapData } from '../lib/chartData.ts';
  import FilterBar from '../components/FilterBar.svelte';
  import SummaryBar from '../components/SummaryBar.svelte';
  import ServicesTable from '../components/ServicesTable.svelte';
  import SourceDot from '../components/SourceDot.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import TreemapChart from '../components/TreemapChart.svelte';
  import CollapsibleSection from '../CollapsibleSection.svelte';

  let { services = [], sourcesInfo = [], discovering = false, initialLoading = false, loadError = null, onRetry = null } = $props();

  let enabledSources = $derived(sourcesInfo.filter((s) => s.enabled));
  let disabledSources = $derived(sourcesInfo.filter((s) => !s.enabled));

  const STATUS_LABELS = { Compliant: 'Compliant', Warning: 'Warning', NonCompliant: 'Non-Compliant', Unknown: 'Unknown', Reference: 'Reference' };
  function statusLabel(s) { return STATUS_LABELS[s] || s; }

  // The visible set is driven entirely by the shared filter store, so the summary
  // (computed from `filtered`) and the table stay in sync with the URL.
  const filters = $derived(getFilters());
  let filtered = $derived(applyFilters(services, filters));

  // Needs attention: non-compliant/warning services, sorted by blast radius
  // descending. Computed from the filtered set so it agrees with the visible
  // table (and the summary) when a filter is active.
  let needsAttention = $derived(
    filtered
      .filter((s) => s.contractStatus === 'NonCompliant' || s.contractStatus === 'Warning')
      .sort((a, b) => (b.blastRadius || 0) - (a.blastRadius || 0))
      .slice(0, 5)
  );
</script>

<div class="list-header">
  <h1>Services {#if services.length > 0}<span class="tab-count">{filtered.length === services.length ? services.length : `${filtered.length} / ${services.length}`}</span>{/if}</h1>
</div>

<!-- Shared filter + summary: the summary reacts to the active filters. -->
{#if services.length > 0}
  <FilterBar {services} />
  <SummaryBar services={filtered} />
{/if}

{#if services.length > 0}
  <div class="cta-row">
    <a href="#/graph" class="graph-cta">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="22" height="22"><circle cx="12" cy="5" r="3"/><circle cx="5" cy="19" r="3"/><circle cx="19" cy="19" r="3"/><line x1="12" y1="8" x2="5" y2="16"/><line x1="12" y1="8" x2="19" y2="16"/></svg>
      <div class="graph-cta-text">
        <span class="graph-cta-title">Dependency Graph</span>
        <span class="graph-cta-desc">Visualize service relationships and blast radius</span>
      </div>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16" style="flex-shrink:0; opacity:0.5"><path d="M9 18l6-6-6-6"/></svg>
    </a>
    <a href="#/owners" class="graph-cta">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="22" height="22"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
      <div class="graph-cta-text">
        <span class="graph-cta-title">Owners</span>
        <span class="graph-cta-desc">Explore services grouped by ownership</span>
      </div>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16" style="flex-shrink:0; opacity:0.5"><path d="M9 18l6-6-6-6"/></svg>
    </a>
    <a href="#/readiness" class="graph-cta">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="22" height="22"><path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>
      <div class="graph-cta-text">
        <span class="graph-cta-title">Service Readiness</span>
        <span class="graph-cta-desc">Operational readiness scores, checks and gaps</span>
      </div>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16" style="flex-shrink:0; opacity:0.5"><path d="M9 18l6-6-6-6"/></svg>
    </a>
  </div>
{/if}

{#if filtered.length > 0}
  <CollapsibleSection title="Fleet risk map" open={true}>
    <TreemapChart data={treemapData(filtered)} onSelect={(name) => location.hash = serviceUrl(name)} />
  </CollapsibleSection>
{/if}

<!-- Needs attention -->
{#if needsAttention.length > 0}
  <div class="alerts">
    <div class="alerts-title">Needs attention</div>
    {#each needsAttention as svc}
      <a href={serviceUrl(svc.name)} class="alert-item" class:alert-err={svc.contractStatus === 'NonCompliant'} class:alert-warn={svc.contractStatus === 'Warning'}>
        <span class="alert-dot" style="background:{svc.contractStatus === 'NonCompliant' ? 'var(--c-err)' : 'var(--c-warn)'}"></span>
        <span class="alert-name">{svc.name}</span>
        <span class="badge badge-{svc.contractStatus === 'NonCompliant' ? 'err' : 'warn'}" style="font-size:10px">{statusLabel(svc.contractStatus)}</span>
        {#if svc.topInsight}<span class="alert-reason">{svc.topInsight}</span>{/if}
        {#if (svc.blastRadius || 0) > 0}
          <span class="blast-pill" class:blast-pill-high={svc.blastRadius >= 5} class:blast-pill-med={svc.blastRadius >= 3 && svc.blastRadius < 5}>
            impacts {svc.blastRadius} service{svc.blastRadius !== 1 ? 's' : ''}
          </span>
        {/if}
      </a>
    {/each}
  </div>
{/if}

<!-- Table -->
{#if services.length === 0 && loadError}
  <EmptyState
    error={loadError}
    onRetry={onRetry}
    title="Can’t reach the backend"
    message="The dashboard couldn’t load services. It will keep retrying." />
{:else if services.length === 0 && (initialLoading || discovering)}
  <EmptyState loading message={initialLoading ? 'Loading services…' : 'Discovering services…'} />
{:else if services.length === 0}
  <div class="state-box">
    <h3>No services found</h3>
    {#if enabledSources.length === 0}
      <p>No data sources are available. Start with one of these:</p>
      <ul class="source-hints">
        <li><strong>Local:</strong> run from a directory containing <code>pacto.yaml</code></li>
        <li><strong>Kubernetes:</strong> ensure a valid kubeconfig and reachable cluster</li>
        <li><strong>OCI:</strong> pass an <code>oci://registry/repo</code> argument</li>
      </ul>
      {#if disabledSources.length > 0}
        <div class="source-reasons">
          {#each disabledSources as src}
            <span class="source-reason"><SourceDot source={src.type} />{src.type}: {src.reason}</span>
          {/each}
        </div>
      {/if}
    {:else}
      <p>Connected sources have no contract data yet.</p>
      {#if enabledSources.length > 0}
        <div class="source-reasons">
          {#each enabledSources as src}
            <span class="source-reason"><SourceDot source={src.type} />{src.type}: {src.reason}</span>
          {/each}
        </div>
      {/if}
    {/if}
  </div>
{:else}
  {#if discovering}
    <div class="discovering-banner">
      <div class="spinner" style="width:14px;height:14px"></div>
      <span>Discovering more services...</span>
    </div>
  {/if}
  <!-- ServicesTable provides its own .table-wrap; a second one here nested two
       overflow contexts and produced a spurious horizontal scrollbar. -->
  <div class="fade-in-up">
    <ServicesTable services={filtered} />
  </div>
{/if}

<style>
  .list-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: var(--sp-4); }

  .cta-row { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-5); flex-wrap: wrap; }
  .cta-row .graph-cta { margin-bottom: 0; flex: 1; min-width: 220px; }
  .graph-cta {
    display: flex; align-items: center; gap: var(--sp-3);
    padding: var(--sp-3) var(--sp-4);
    min-height: var(--touch-min);
    border: 1px solid var(--c-accent); border-radius: var(--radius-sm);
    background: var(--c-accent-bg); color: var(--c-accent);
    text-decoration: none; margin-bottom: var(--sp-5);
    transition: all var(--transition);
  }
  .graph-cta:hover {
    background: var(--c-accent); color: var(--c-on-accent);
    text-decoration: none; box-shadow: var(--shadow-md);
  }
  .graph-cta-text { flex: 1; }
  .graph-cta-title { display: block; font-weight: 600; font-size: var(--text-sm); }
  .graph-cta-desc { display: block; font-size: var(--text-xs); opacity: 0.8; margin-top: 2px; }

  .alerts { display: flex; flex-direction: column; gap: var(--sp-2); margin-bottom: var(--sp-5); }
  .alerts-title { font-size: var(--text-xs); font-weight: 600; color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: var(--sp-1); }
  .alert-item {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-3); border-radius: var(--radius-xs);
    font-size: var(--text-sm);
    text-decoration: none; color: var(--c-text);
    transition: opacity var(--transition);
    min-height: var(--touch-min);
  }
  .alert-err { background: var(--c-err-bg); }
  .alert-warn { background: var(--c-warn-bg); }
  .alert-item:hover { text-decoration: none; opacity: 0.85; }
  .alert-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
  .alert-name { font-weight: 600; }
  .alert-reason { color: var(--c-text-2); }

  .blast-pill {
    font-size: var(--text-xs); font-weight: 500;
    padding: 2px 8px; border-radius: var(--radius-xs);
    background: var(--c-warn-bg); color: var(--c-warn);
  }
  .blast-pill-med { background: var(--c-warn-bg); color: var(--c-warn); }
  .blast-pill-high { background: var(--c-err-bg); color: var(--c-err); }


  .source-hints {
    list-style: none; text-align: left; margin-top: var(--sp-2);
    display: flex; flex-direction: column; gap: var(--sp-2);
    font-size: var(--text-sm); color: var(--c-text-2);
  }
  .source-hints li::before { content: '→ '; color: var(--c-text-3); }
  .source-reasons {
    display: flex; flex-direction: column; gap: var(--sp-2);
    margin-top: var(--sp-3); font-size: var(--text-xs); color: var(--c-text-3);
  }
  .source-reason {
    display: inline-flex; align-items: center; gap: 6px;
  }
  .discovering-banner {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-3); margin-bottom: var(--sp-3);
    border-radius: var(--radius-sm);
    background: var(--c-accent-bg); border: 1px solid var(--c-accent);
    color: var(--c-accent); font-size: var(--text-sm); font-weight: 500;
  }

  /* ─── Mobile ─── */
  @media (max-width: 768px) {
    .alert-item { flex-wrap: wrap; }
    .alert-reason { width: 100%; font-size: var(--text-xs); margin-top: 2px; }
  }
</style>
