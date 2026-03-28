<script>
  import { serviceUrl } from '../lib/router.ts';
  import { statusClass, complianceStatusClass, sourceTooltip } from '../lib/format.ts';
  import StatsBar from '../StatsBar.svelte';

  let { services = [], sourcesInfo = [], discovering = false } = $props();

  let enabledSources = $derived(sourcesInfo.filter((s) => s.enabled));
  let disabledSources = $derived(sourcesInfo.filter((s) => !s.enabled));

  let nameFilter = $state('');
  let statusFilter = $state('all');
  let sourceFilter = $state('all');
  let sortBy = $state('name');
  let sortAsc = $state(true);

  const STATUS_LABELS = { Compliant: 'Compliant', Warning: 'Warning', NonCompliant: 'Non-Compliant', Unknown: 'Unknown', Reference: 'Reference' };
  function statusLabel(s) { return STATUS_LABELS[s] || s; }

  // Filter + sort
  let filtered = $derived.by(() => {
    let list = services;
    if (nameFilter) {
      const q = nameFilter.toLowerCase();
      list = list.filter((s) => s.name.toLowerCase().includes(q) || (s.owner || '').toLowerCase().includes(q));
    }
    if (statusFilter !== 'all') {
      list = list.filter((s) => s.contractStatus === statusFilter);
    }
    if (sourceFilter !== 'all') {
      list = list.filter((s) => (s.sources || [s.source]).includes(sourceFilter));
    }
    const dir = sortAsc ? 1 : -1;
    list = [...list].sort((a, b) => {
      if (sortBy === 'name') return a.name.localeCompare(b.name) * dir;
      if (sortBy === 'status') return (a.contractStatus || '').localeCompare(b.contractStatus || '') * dir;
      if (sortBy === 'compliance') return ((a.complianceScore ?? -1) - (b.complianceScore ?? -1)) * dir;
      if (sortBy === 'blast') return ((a.blastRadius || 0) - (b.blastRadius || 0)) * dir;
      return 0;
    });
    return list;
  });

  // Needs attention: non-compliant/warning services, sorted by blast radius descending
  let needsAttention = $derived(
    services
      .filter((s) => s.contractStatus === 'NonCompliant' || s.contractStatus === 'Warning')
      .sort((a, b) => (b.blastRadius || 0) - (a.blastRadius || 0))
      .slice(0, 5)
  );

  function toggleSort(col) {
    if (sortBy === col) sortAsc = !sortAsc;
    else { sortBy = col; sortAsc = true; }
  }

  function sortIcon(col) {
    if (sortBy !== col) return '';
    return sortAsc ? ' ↑' : ' ↓';
  }
</script>

<div class="list-header">
  <h1>Services</h1>
</div>

<!-- Stats bar -->
<StatsBar {services} bind:statusFilter bind:sourceFilter bind:nameFilter />

{#if services.length > 0}
  <a href="#/graph" class="graph-cta">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="22" height="22"><circle cx="12" cy="5" r="3"/><circle cx="5" cy="19" r="3"/><circle cx="19" cy="19" r="3"/><line x1="12" y1="8" x2="5" y2="16"/><line x1="12" y1="8" x2="19" y2="16"/></svg>
    <div class="graph-cta-text">
      <span class="graph-cta-title">Dependency Graph</span>
      <span class="graph-cta-desc">Visualize service relationships and blast radius</span>
    </div>
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16" style="flex-shrink:0; opacity:0.5"><path d="M9 18l6-6-6-6"/></svg>
  </a>
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
        {#if (svc.blastRadius || 0) > 0}<span class="pill">blast: {svc.blastRadius}</span>{/if}
      </a>
    {/each}
  </div>
{/if}

<!-- Table -->
{#if services.length === 0}
  <div class="state-box">
    {#if discovering}
      <div class="skeleton-table fade-in">
        {#each Array(5) as _}
          <div class="skeleton-row">
            <div class="skeleton skeleton-line" style="width:30%"></div>
            <div class="skeleton skeleton-line" style="width:15%"></div>
            <div class="skeleton skeleton-line" style="width:20%"></div>
          </div>
        {/each}
      </div>
      <p style="margin-top:var(--sp-3); color:var(--c-text-3)">Discovering services…</p>
    {:else}
      <h3>No services found</h3>
      {#if enabledSources.length === 0}
        <p>No data sources are available. Start with one of these:</p>
        <ul class="source-hints">
          <li><strong>Local:</strong> run from a directory containing <code>pacto.yaml</code></li>
          <li><strong>Kubernetes:</strong> ensure a valid kubeconfig and reachable cluster</li>
          <li><strong>OCI:</strong> specify a registry with <code>--repo</code></li>
        </ul>
        {#if disabledSources.length > 0}
          <div class="source-reasons">
            {#each disabledSources as src}
              <span class="source-reason"><span class="source-dot source-dot-{src.type}"></span>{src.type}: {src.reason}</span>
            {/each}
          </div>
        {/if}
      {:else}
        <p>Connected sources have no contract data yet.</p>
        {#if enabledSources.length > 0}
          <div class="source-reasons">
            {#each enabledSources as src}
              <span class="source-reason"><span class="source-dot source-dot-{src.type}"></span>{src.type}: {src.reason}</span>
            {/each}
          </div>
        {/if}
      {/if}
    {/if}
  </div>
{:else if filtered.length === 0}
  <div class="state-box">
    <h3>No matching services</h3>
    <p>Try a different search or filter.</p>
  </div>
{:else}
  {#if discovering}
    <div class="discovering-banner">
      <div class="spinner" style="width:14px;height:14px"></div>
      <span>Discovering more services...</span>
    </div>
  {/if}
  <div class="table-wrap fade-in-up">
    <table>
      <thead>
        <tr>
          <th><button type="button" class="col-sort" data-tip="Service contract name" onclick={() => toggleSort('name')}>Name{sortIcon('name')}</button></th>
          <th data-tip="Current contract version">Version</th>
          <th><button type="button" class="col-sort" data-tip="Contract compliance status" onclick={() => toggleSort('status')}>Contract Status{sortIcon('status')}</button></th>
          <th><button type="button" class="col-sort" data-tip="Contract compliance score (0–100%)" onclick={() => toggleSort('compliance')}>Compliance{sortIcon('compliance')}</button></th>
          <th data-tip="Validation checks passed / total">Checks</th>
          <th><button type="button" class="col-sort" data-tip="Number of services impacted if this one fails" onclick={() => toggleSort('blast')}>Blast{sortIcon('blast')}</button></th>
          <th data-tip="Data source: k8s, oci, or local">Source</th>
        </tr>
      </thead>
      <tbody>
        {#each filtered as svc}
          <tr class="clickable" onclick={() => location.hash = serviceUrl(svc.name).slice(0)}>
            <td>
              <a href={serviceUrl(svc.name)} class="svc-name">{svc.name}</a>
              {#if svc.owner}<span class="svc-owner">{svc.owner}</span>{/if}
            </td>
            <td><span class="pill">{svc.version || '—'}</span></td>
            <td><span class="badge badge-{statusClass(svc.contractStatus)}"><span class="badge-dot"></span>{statusLabel(svc.contractStatus)}</span></td>
            <td>
              {#if svc.complianceScore != null}
                <span class="score {complianceStatusClass(svc.complianceStatus)}">{svc.complianceScore}%</span>
              {:else}
                <span class="text-dim">—</span>
              {/if}
            </td>
            <td>
              {#if svc.checksTotal > 0}
                <span class:text-ok={svc.checksFailed === 0} class:text-err={svc.checksFailed > 0}>
                  {svc.checksPassed}/{svc.checksTotal}
                </span>
              {:else}
                <span class="text-dim">—</span>
              {/if}
            </td>
            <td>
              {#if svc.blastRadius > 0}
                <span class:text-warn={svc.blastRadius >= 3}>{svc.blastRadius}</span>
              {:else}
                <span class="text-dim">0</span>
              {/if}
            </td>
            <td>
              {#each (svc.sources || [svc.source]) as src}
                <span class="source-dot source-dot-{src}" data-tip={sourceTooltip(src)} data-tip-align="right"></span>
              {/each}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .list-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: var(--sp-5); }
  .graph-cta {
    display: flex; align-items: center; gap: var(--sp-3);
    padding: var(--sp-3) var(--sp-4);
    border: 1px solid var(--c-accent); border-radius: var(--radius-sm);
    background: var(--c-accent-bg); color: var(--c-accent);
    text-decoration: none; margin-bottom: var(--sp-5);
    transition: all var(--transition);
  }
  .graph-cta:hover {
    background: var(--c-accent); color: white;
    text-decoration: none; box-shadow: var(--shadow-md);
  }
  .graph-cta-text { flex: 1; }
  .graph-cta-title { display: block; font-weight: 600; font-size: var(--text-sm); }
  .graph-cta-desc { display: block; font-size: var(--text-xs); opacity: 0.8; }

  .alerts { display: flex; flex-direction: column; gap: var(--sp-1); margin-bottom: var(--sp-5); }
  .alerts-title { font-size: var(--text-xs); font-weight: 600; color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: var(--sp-1); }
  .alert-item {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-xs);
    font-size: var(--text-sm);
    text-decoration: none; color: var(--c-text);
    transition: opacity var(--transition);
  }
  .alert-err { background: var(--c-err-bg); }
  .alert-warn { background: var(--c-warn-bg); }
  .alert-item:hover { text-decoration: none; opacity: 0.85; }
  .alert-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
  .alert-name { font-weight: 600; }
  .alert-reason { color: var(--c-text-2); }

  .svc-name { font-weight: 600; text-decoration: none; }
  .svc-name:hover { text-decoration: underline; }
  .svc-owner { color: var(--c-text-3); font-size: var(--text-xs); margin-left: 6px; }

  .col-sort {
    background: none; border: none; padding: 0; font: inherit;
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); cursor: pointer;
    white-space: nowrap;
  }
  .col-sort:hover { color: var(--c-text); }

  .text-dim { color: var(--c-text-3); }
  .text-ok { color: var(--c-ok); }
  .text-err { color: var(--c-err); }
  .text-warn { color: var(--c-warn); }

  .skeleton-table { width: 100%; max-width: 600px; }
  .skeleton-row { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-3); }
  .skeleton-row .skeleton-line { height: 16px; border-radius: var(--radius-xs); }

  .source-hints {
    list-style: none; text-align: left; margin-top: var(--sp-2);
    display: flex; flex-direction: column; gap: var(--sp-1);
    font-size: var(--text-sm); color: var(--c-text-2);
  }
  .source-hints li::before { content: '→ '; color: var(--c-text-3); }
  .source-reasons {
    display: flex; flex-direction: column; gap: var(--sp-1);
    margin-top: var(--sp-3); font-size: var(--text-xs); color: var(--c-text-3);
  }
  .source-reason {
    display: inline-flex; align-items: center; gap: 6px;
  }
  .discovering-banner {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3); margin-bottom: var(--sp-3);
    border-radius: var(--radius-sm);
    background: var(--c-accent-bg); border: 1px solid var(--c-accent);
    color: var(--c-accent); font-size: var(--text-sm); font-weight: 500;
  }
</style>
