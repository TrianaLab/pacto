<script>
  import { serviceUrl } from '../lib/router.js';

  let { services = [], sourcesInfo = [], discovering = false } = $props();

  let search = $state('');
  let phaseFilter = $state('all');
  let sortBy = $state('name');
  let sortAsc = $state(true);

  // Stats
  let stats = $derived.by(() => {
    const s = { total: services.length, healthy: 0, degraded: 0, invalid: 0, unknown: 0 };
    for (const svc of services) {
      if (svc.phase === 'Healthy') s.healthy++;
      else if (svc.phase === 'Degraded') s.degraded++;
      else if (svc.phase === 'Invalid') s.invalid++;
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
      list = list.filter((s) => {
        if (phaseFilter === 'Unknown') return s.phase === 'Unknown' || s.phase === 'Reference';
        return s.phase === phaseFilter;
      });
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

  // At-risk: Invalid or high blast radius
  let atRisk = $derived(
    services.filter((s) => s.phase === 'Invalid' || (s.blastRadius || 0) >= 3).slice(0, 5)
  );

  function toggleSort(col) {
    if (sortBy === col) sortAsc = !sortAsc;
    else { sortBy = col; sortAsc = true; }
  }

  function sortIcon(col) {
    if (sortBy !== col) return '';
    return sortAsc ? ' ↑' : ' ↓';
  }

  function complianceClass(status) {
    if (status === 'OK') return 'score-ok';
    if (status === 'WARNING') return 'score-warn';
    if (status === 'ERROR') return 'score-err';
    return '';
  }

  function phaseClass(phase) {
    if (phase === 'Healthy') return 'ok';
    if (phase === 'Degraded') return 'warn';
    if (phase === 'Invalid') return 'err';
    return 'neutral';
  }
</script>

<div class="list-header">
  <h1>Services</h1>
  <a href="#/graph" class="btn btn-sm">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="12" cy="5" r="3"/><circle cx="5" cy="19" r="3"/><circle cx="19" cy="19" r="3"/><line x1="12" y1="8" x2="5" y2="16"/><line x1="12" y1="8" x2="19" y2="16"/></svg>
    Graph
  </a>
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
    {#if stats.unknown > 0}
      <button type="button" class="stat" class:stat-active={phaseFilter === 'Unknown'} onclick={() => phaseFilter = phaseFilter === 'Unknown' ? 'all' : 'Unknown'}>
        <span class="stat-value" style="color:var(--c-neutral)">{stats.unknown}</span>
        <span class="stat-label">Unknown</span>
      </button>
    {/if}
  </div>
{/if}

<!-- At-risk alerts -->
{#if atRisk.length > 0}
  <div class="alerts">
    {#each atRisk as svc}
      <a href={serviceUrl(svc.name)} class="alert-item">
        {#if svc.phase === 'Invalid'}
          <span class="alert-dot" style="background:var(--c-err)"></span>
        {:else}
          <span class="alert-dot" style="background:var(--c-warn)"></span>
        {/if}
        <span class="alert-name">{svc.name}</span>
        {#if svc.topInsight}<span class="alert-reason">{svc.topInsight}</span>{/if}
        {#if (svc.blastRadius || 0) >= 3}<span class="pill">blast: {svc.blastRadius}</span>{/if}
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
      <div class="spinner"></div>
      <h3>Discovering services…</h3>
      <p>Looking for contracts in connected sources.</p>
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
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th><button type="button" class="col-sort" onclick={() => toggleSort('name')}>Name{sortIcon('name')}</button></th>
          <th>Version</th>
          <th><button type="button" class="col-sort" onclick={() => toggleSort('phase')}>Status{sortIcon('phase')}</button></th>
          <th><button type="button" class="col-sort" onclick={() => toggleSort('compliance')}>Compliance{sortIcon('compliance')}</button></th>
          <th>Checks</th>
          <th><button type="button" class="col-sort" onclick={() => toggleSort('blast')}>Blast{sortIcon('blast')}</button></th>
          <th>Source</th>
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
                <span class="score {complianceClass(svc.complianceStatus)}">{svc.complianceScore}%</span>
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
                <span class="source-dot source-dot-{src}" title={src}></span>
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

  .alerts { display: flex; flex-direction: column; gap: var(--sp-1); margin-bottom: var(--sp-5); }
  .alert-item {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-xs);
    background: var(--c-err-bg); font-size: var(--text-sm);
    text-decoration: none; color: var(--c-text);
  }
  .alert-item:hover { text-decoration: none; opacity: 0.9; }
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
</style>
