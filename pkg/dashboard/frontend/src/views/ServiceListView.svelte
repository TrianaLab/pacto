<script>
  import { serviceUrl } from '../lib/router.js';
  import { phaseClass, complianceStatusClass, sourceTooltip } from '../lib/format.js';

  let { services = [], sourcesInfo = [], discovering = false } = $props();

  let search = $state('');
  let phaseFilter = $state('all');
  let sortBy = $state('name');
  let sortAsc = $state(true);

  // Stats
  let stats = $derived.by(() => {
    const s = { total: services.length, healthy: 0, degraded: 0, invalid: 0, unknown: 0, reference: 0 };
    for (const svc of services) {
      if (svc.phase === 'Healthy') s.healthy++;
      else if (svc.phase === 'Degraded') s.degraded++;
      else if (svc.phase === 'Invalid') s.invalid++;
      else if (svc.phase === 'Reference') s.reference++;
      else s.unknown++;
    }
    return s;
  });

  // Filter + sort
  let filtered = $derived.by(() => {
    let list = services;
    if (search) {
      const q = search.toLowerCase();
      list = list.filter((s) => s.name.toLowerCase().includes(q) || (s.owner || '').toLowerCase().includes(q));
    }
    if (phaseFilter !== 'all') {
      list = list.filter((s) => s.phase === phaseFilter);
    }
    const dir = sortAsc ? 1 : -1;
    list = [...list].sort((a, b) => {
      if (sortBy === 'name') return a.name.localeCompare(b.name) * dir;
      if (sortBy === 'phase') return a.phase.localeCompare(b.phase) * dir;
      if (sortBy === 'compliance') return ((a.complianceScore ?? -1) - (b.complianceScore ?? -1)) * dir;
      if (sortBy === 'blast') return ((a.blastRadius || 0) - (b.blastRadius || 0)) * dir;
      return 0;
    });
    return list;
  });

  // Needs attention: degraded/invalid services, sorted by blast radius descending
  let needsAttention = $derived(
    services
      .filter((s) => s.phase === 'Invalid' || s.phase === 'Degraded')
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
{#if stats.total > 0}
  <div class="stats-bar">
    <button type="button" class="stat" class:stat-active={phaseFilter === 'all'} onclick={() => phaseFilter = 'all'}>
      <span class="stat-value">{stats.total}</span>
      <span class="stat-label">Total</span>
    </button>
    <button type="button" class="stat" class:stat-active={phaseFilter === 'Healthy'} onclick={() => phaseFilter = phaseFilter === 'Healthy' ? 'all' : 'Healthy'}>
      <span class="stat-value" style="color:var(--c-ok)">{stats.healthy}</span>
      <span class="stat-label">Healthy</span>
    </button>
    <button type="button" class="stat" class:stat-active={phaseFilter === 'Degraded'} onclick={() => phaseFilter = phaseFilter === 'Degraded' ? 'all' : 'Degraded'}>
      <span class="stat-value" style="color:var(--c-warn)">{stats.degraded}</span>
      <span class="stat-label">Degraded</span>
    </button>
    <button type="button" class="stat" class:stat-active={phaseFilter === 'Invalid'} onclick={() => phaseFilter = phaseFilter === 'Invalid' ? 'all' : 'Invalid'}>
      <span class="stat-value" style="color:var(--c-err)">{stats.invalid}</span>
      <span class="stat-label">Invalid</span>
    </button>
    {#if stats.reference > 0}
      <button type="button" class="stat" class:stat-active={phaseFilter === 'Reference'} onclick={() => phaseFilter = phaseFilter === 'Reference' ? 'all' : 'Reference'}>
        <span class="stat-value" style="color:var(--c-info)">{stats.reference}</span>
        <span class="stat-label">Reference</span>
      </button>
    {/if}
    {#if stats.unknown > 0}
      <button type="button" class="stat" class:stat-active={phaseFilter === 'Unknown'} onclick={() => phaseFilter = phaseFilter === 'Unknown' ? 'all' : 'Unknown'}>
        <span class="stat-value" style="color:var(--c-neutral)">{stats.unknown}</span>
        <span class="stat-label">Unknown</span>
      </button>
    {/if}
  </div>
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
      <a href={serviceUrl(svc.name)} class="alert-item" class:alert-err={svc.phase === 'Invalid'} class:alert-warn={svc.phase === 'Degraded'}>
        <span class="alert-dot" style="background:{svc.phase === 'Invalid' ? 'var(--c-err)' : 'var(--c-warn)'}"></span>
        <span class="alert-name">{svc.name}</span>
        <span class="badge badge-{svc.phase === 'Invalid' ? 'err' : 'warn'}" style="font-size:10px">{svc.phase}</span>
        {#if svc.topInsight}<span class="alert-reason">{svc.topInsight}</span>{/if}
        {#if (svc.blastRadius || 0) > 0}<span class="pill">blast: {svc.blastRadius}</span>{/if}
      </a>
    {/each}
  </div>
{/if}

<!-- Search / Filter -->
<div class="toolbar">
  <input class="input" type="text" placeholder="Filter services…" bind:value={search} aria-label="Filter services" />
</div>

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
      <p>Connect a source (Kubernetes, OCI registry, or local directory) to see contracts.</p>
    {/if}
  </div>
{:else if filtered.length === 0}
  <div class="state-box">
    <h3>No matching services</h3>
    <p>Try a different search or filter.</p>
  </div>
{:else}
  <div class="table-wrap fade-in-up">
    <table>
      <thead>
        <tr>
          <th><button type="button" class="col-sort" data-tip="Service contract name" onclick={() => toggleSort('name')}>Name{sortIcon('name')}</button></th>
          <th data-tip="Current contract version">Version</th>
          <th><button type="button" class="col-sort" data-tip="Service health phase" onclick={() => toggleSort('phase')}>Status{sortIcon('phase')}</button></th>
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
            <td><span class="badge badge-{phaseClass(svc.phase)}"><span class="badge-dot"></span>{svc.phase}</span></td>
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
  .stats-bar { display: flex; gap: var(--sp-2); margin-bottom: var(--sp-5); flex-wrap: wrap; }
  .stat {
    display: flex; flex-direction: column; align-items: center; gap: 2px;
    padding: var(--sp-3) var(--sp-5); border-radius: var(--radius-sm);
    background: var(--c-surface); border: 1px solid var(--c-border);
    cursor: pointer; font: inherit; color: var(--c-text);
    min-width: 80px; transition: border-color var(--transition);
  }
  .stat:hover { border-color: var(--c-text-3); }
  .stat-active { border-color: var(--c-accent); background: var(--c-accent-bg); }
  .stat-value { font-size: var(--text-lg); font-weight: 700; }
  .stat-label { font-size: var(--text-xs); color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.05em; }
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

  .toolbar { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-4); }
  .toolbar .input { flex: 1; max-width: 360px; }

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
</style>
