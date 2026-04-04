<script>
  import { ownerUrl, serviceUrl } from '../lib/router.ts';
  import { aggregateByOwner, complianceClass, statusClass, ownerKey, sourceTooltip, complianceStatusClass } from '../lib/format.ts';
  import OwnerBarChart from '../OwnerBarChart.svelte';

  let { services = [], initialLoading = false } = $props();

  let sortBy = $state('services');
  let sortAsc = $state(false);
  let nameFilter = $state('');
  let statusFilter = $state('all'); // all | warnings | non-compliant | compliant
  let expandedOwner = $state(null);

  // Services belonging to the expanded owner
  let expandedServices = $derived.by(() => {
    if (!expandedOwner) return [];
    return services.filter((s) => (ownerKey(s.owner) || '(unowned)') === expandedOwner);
  });

  function toggleExpand(key) {
    expandedOwner = expandedOwner === key ? null : key;
  }

  // Single derived list used by both chart and table
  let owners = $derived.by(() => {
    let list = aggregateByOwner(services);

    // Text filter
    if (nameFilter) {
      const q = nameFilter.toLowerCase();
      list = list.filter((o) => o.key.toLowerCase().includes(q));
    }

    // Status filter
    if (statusFilter === 'warnings') list = list.filter((o) => o.warning > 0);
    else if (statusFilter === 'non-compliant') list = list.filter((o) => o.nonCompliant > 0);
    else if (statusFilter === 'compliant') list = list.filter((o) => o.compliancePercent === 100);

    // Sort
    const dir = sortAsc ? 1 : -1;
    return [...list].sort((a, b) => {
      if (sortBy === 'key') return a.key.localeCompare(b.key) * dir;
      if (sortBy === 'services') return (a.services - b.services) * dir;
      if (sortBy === 'compliance') return (a.compliancePercent - b.compliancePercent) * dir;
      if (sortBy === 'blast') return (a.totalBlast - b.totalBlast) * dir;
      if (sortBy === 'warning') return (a.warning - b.warning) * dir;
      if (sortBy === 'nonCompliant') return (a.nonCompliant - b.nonCompliant) * dir;
      return 0;
    });
  });

  // Totals for filter pills — filtered by name (but not status) so counts update dynamically
  let allOwners = $derived(aggregateByOwner(services));
  let nameFilteredOwners = $derived.by(() => {
    if (!nameFilter) return allOwners;
    const q = nameFilter.toLowerCase();
    return allOwners.filter((o) => o.key.toLowerCase().includes(q));
  });
  let filterCounts = $derived.by(() => {
    let warnings = 0, nonCompliant = 0, compliant = 0;
    for (const o of nameFilteredOwners) {
      if (o.warning > 0) warnings++;
      if (o.nonCompliant > 0) nonCompliant++;
      if (o.compliancePercent === 100) compliant++;
    }
    return { warnings, nonCompliant, compliant };
  });

  function setSort(col) {
    if (sortBy === col) sortAsc = !sortAsc;
    else { sortBy = col; sortAsc = col === 'key'; }
  }

  function sortIcon(col) {
    if (sortBy !== col) return '';
    return sortAsc ? ' ↑' : ' ↓';
  }

  function toggleFilter(f) {
    statusFilter = statusFilter === f ? 'all' : f;
  }

  const SORT_OPTIONS = [
    { value: 'services', label: '# Services' },
    { value: 'blast', label: 'Blast radius' },
    { value: 'compliance', label: '% Compliant' },
    { value: 'warning', label: 'Warnings' },
    { value: 'nonCompliant', label: 'Non-Compliant' },
    { value: 'key', label: 'Name' },
  ];
</script>

<div class="page-header">
  <a href="#/" class="btn btn-sm btn-ghost">← Services</a>
  <h1>Owners</h1>
  <span class="tab-count">{allOwners.length}</span>
</div>

{#if allOwners.length === 0}
  <div class="state-box">
    {#if initialLoading}
      <div class="skeleton-table fade-in">
        {#each Array(4) as _}
          <div class="skeleton-row">
            <div class="skeleton skeleton-line" style="width:25%"></div>
            <div class="skeleton skeleton-line" style="width:10%"></div>
            <div class="skeleton skeleton-line" style="width:15%"></div>
          </div>
        {/each}
      </div>
      <p style="margin-top:var(--sp-3); color:var(--c-text-3)">Loading owners…</p>
    {:else}
      <h3>No ownership data</h3>
      <p>Services don't have owner fields set. Add <code>owner</code> to your contracts.</p>
    {/if}
  </div>
{:else}
  <!-- Controls -->
  <div class="controls-row">
    <div class="controls-left">
      <span class="control-label">Sort</span>
      {#each SORT_OPTIONS as opt}
        <button type="button" class="sort-chip" class:active={sortBy === opt.value} onclick={() => setSort(opt.value)}>
          {opt.label}{#if sortBy === opt.value}<span class="sort-arrow">{sortAsc ? '↑' : '↓'}</span>{/if}
        </button>
      {/each}

      <span class="controls-sep"></span>

      {#if filterCounts.warnings > 0}
        <button type="button" class="filter-chip" class:active={statusFilter === 'warnings'} onclick={() => toggleFilter('warnings')}>
          <span class="chip-dot" style="background:var(--c-warn)"></span>
          Warnings <span class="chip-count">{filterCounts.warnings}</span>
        </button>
      {/if}
      {#if filterCounts.nonCompliant > 0}
        <button type="button" class="filter-chip" class:active={statusFilter === 'non-compliant'} onclick={() => toggleFilter('non-compliant')}>
          <span class="chip-dot" style="background:var(--c-err)"></span>
          Non-Compliant <span class="chip-count">{filterCounts.nonCompliant}</span>
        </button>
      {/if}
      {#if filterCounts.compliant > 0}
        <button type="button" class="filter-chip" class:active={statusFilter === 'compliant'} onclick={() => toggleFilter('compliant')}>
          <span class="chip-dot" style="background:var(--c-ok)"></span>
          Fully Compliant <span class="chip-count">{filterCounts.compliant}</span>
        </button>
      {/if}
    </div>

    <div class="filter-search">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="12" height="12"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
      <input type="text" placeholder="Filter owners…" bind:value={nameFilter} aria-label="Filter by owner name" />
    </div>
  </div>

  {#if owners.length === 0}
    <div class="state-box">
      <h3>No matching owners</h3>
      <p>Try a different search or filter.</p>
    </div>
  {:else}
    <!-- Chart -->
    {#if owners.length > 1}
      <OwnerBarChart {owners} {sortBy} />
    {/if}

    <!-- Table -->
    <div class="table-wrap fade-in-up">
      <table>
        <thead>
          <tr>
            <th><button type="button" class="col-sort" onclick={() => setSort('key')}>Owner{sortIcon('key')}</button></th>
            <th><button type="button" class="col-sort" onclick={() => setSort('services')}># Services{sortIcon('services')}</button></th>
            <th data-tip="Compliant services">Compliant</th>
            <th data-tip="Services with warnings"><button type="button" class="col-sort" onclick={() => setSort('warning')}>Warning{sortIcon('warning')}</button></th>
            <th data-tip="Non-compliant services"><button type="button" class="col-sort" onclick={() => setSort('nonCompliant')}>Non-Compliant{sortIcon('nonCompliant')}</button></th>
            <th data-tip="Reference-only contracts">Reference</th>
            <th><button type="button" class="col-sort" data-tip="% of assessed services that are compliant" onclick={() => setSort('compliance')}>% Compliant{sortIcon('compliance')}</button></th>
            <th><button type="button" class="col-sort" data-tip="Combined blast radius of all services" onclick={() => setSort('blast')}>Blast{sortIcon('blast')}</button></th>
          </tr>
        </thead>
        <tbody>
          {#each owners as row}
            <tr class="clickable" class:row-expanded={expandedOwner === row.key} onclick={() => toggleExpand(row.key)}>
              <td>
                <span class="expand-icon" class:expanded={expandedOwner === row.key}>›</span>
                <a href={ownerUrl(row.key)} class="owner-name" onclick={(e) => e.stopPropagation()}>{row.key}</a>
              </td>
              <td>{row.services}</td>
              <td>
                {#if row.compliant > 0}<span class="text-ok">{row.compliant}</span>{:else}<span class="text-dim">0</span>{/if}
              </td>
              <td>
                {#if row.warning > 0}<span class="text-warn">{row.warning}</span>{:else}<span class="text-dim">0</span>{/if}
              </td>
              <td>
                {#if row.nonCompliant > 0}<span class="text-err">{row.nonCompliant}</span>{:else}<span class="text-dim">0</span>{/if}
              </td>
              <td>
                {#if row.reference > 0}{row.reference}{:else}<span class="text-dim">0</span>{/if}
              </td>
              <td>
                {#if row.compliancePercent >= 0}
                  <span class="score {complianceClass(row.compliancePercent)}">{row.compliancePercent}%</span>
                {:else}
                  <span class="text-dim">—</span>
                {/if}
              </td>
              <td>
                {#if row.totalBlast > 0}
                  <span class="blast-badge" class:blast-low={row.totalBlast < 3} class:blast-med={row.totalBlast >= 3 && row.totalBlast < 5} class:blast-high={row.totalBlast >= 5}>{row.totalBlast}</span>
                {:else}
                  <span class="text-dim">0</span>
                {/if}
              </td>
            </tr>
            {#if expandedOwner === row.key}
              <tr class="expand-row">
                <td colspan="8">
                  <div class="expand-panel">
                    <table class="expand-table">
                      <thead>
                        <tr>
                          <th>Service</th>
                          <th>Version</th>
                          <th>Status</th>
                          <th data-tip="Compliance score">Compliance</th>
                          <th data-tip="Blast radius">Blast</th>
                          <th data-tip="Data source">Source</th>
                        </tr>
                      </thead>
                      <tbody>
                        {#each expandedServices as svc}
                          <tr class="clickable" onclick={() => location.hash = serviceUrl(svc.name)}>
                            <td><a href={serviceUrl(svc.name)} onclick={(e) => e.stopPropagation()}>{svc.name}</a></td>
                            <td><span class="pill">{svc.version || '—'}</span></td>
                            <td><span class="badge badge-{statusClass(svc.contractStatus)}"><span class="badge-dot"></span>{svc.contractStatus}</span></td>
                            <td>
                              {#if svc.complianceScore != null}
                                <span class="score {complianceStatusClass(svc.complianceStatus)}">{svc.complianceScore}%</span>
                              {:else}
                                <span class="text-dim">—</span>
                              {/if}
                            </td>
                            <td>
                              {#if (svc.blastRadius || 0) > 0}
                                <span class="blast-badge" class:blast-low={svc.blastRadius < 3} class:blast-med={svc.blastRadius >= 3 && svc.blastRadius < 5} class:blast-high={svc.blastRadius >= 5}>{svc.blastRadius}</span>
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
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
{/if}

<style>
  .page-header {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-5); flex-wrap: wrap;
  }

  /* ── Controls ── */
  .controls-row {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-4); flex-wrap: wrap;
  }
  .controls-left {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; flex: 1;
  }

  .control-label {
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3);
  }
  .sort-chip {
    display: inline-flex; align-items: center; gap: 3px;
    padding: 4px 10px; border-radius: 100px;
    border: 1px solid var(--c-border); background: var(--c-surface);
    font: inherit; font-size: var(--text-xs); color: var(--c-text-3);
    cursor: pointer; transition: all var(--transition);
    white-space: nowrap; min-height: 30px;
  }
  .sort-chip:hover { border-color: var(--c-text-3); color: var(--c-text); }
  .sort-chip.active { border-color: var(--c-accent); background: var(--c-accent-bg); color: var(--c-accent); font-weight: 600; }
  .sort-arrow { font-weight: 400; margin-left: 1px; }
  .controls-sep {
    width: 1px; height: 20px; background: var(--c-border); flex-shrink: 0;
  }

  .filter-chip {
    display: inline-flex; align-items: center; gap: 5px;
    padding: 4px 10px; border-radius: 100px;
    border: 1px solid var(--c-border); background: var(--c-surface);
    font: inherit; font-size: var(--text-xs); color: var(--c-text-2);
    cursor: pointer; transition: all var(--transition);
    white-space: nowrap; min-height: 30px;
  }
  .filter-chip:hover { border-color: var(--c-text-3); color: var(--c-text); }
  .filter-chip.active { border-color: var(--c-accent); background: var(--c-accent-bg); color: var(--c-accent); }
  .chip-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
  .chip-count { font-weight: 600; }

  .filter-search {
    display: inline-flex; align-items: center; gap: 5px;
    padding: 4px 10px; border-radius: 100px;
    border: 1px solid var(--c-border); background: var(--c-surface);
    transition: border-color var(--transition);
    min-height: 30px;
  }
  .filter-search:focus-within { border-color: var(--c-accent); }
  .filter-search svg { color: var(--c-text-3); flex-shrink: 0; }
  .filter-search input {
    border: none; background: none; outline: none;
    font: inherit; font-size: var(--text-xs); color: var(--c-text);
    width: 110px; padding: 2px 0;
  }
  .filter-search input::placeholder { color: var(--c-text-3); }

  /* ── Table ── */
  .owner-name { font-weight: 600; text-decoration: none; }
  .owner-name:hover { text-decoration: underline; }

  .col-sort {
    background: none; border: none; padding: 0; font: inherit;
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); cursor: pointer;
    white-space: nowrap;
  }
  .col-sort:hover { color: var(--c-text); }

  .text-dim { color: var(--c-text-3); }
  .text-ok { color: var(--c-ok); }
  .text-warn { color: var(--c-warn); }
  .text-err { color: var(--c-err); }

  .blast-badge {
    display: inline-flex; align-items: center; justify-content: center;
    min-width: 26px; height: 22px; padding: 0 7px;
    border-radius: var(--radius-xs);
    font-size: var(--text-xs); font-weight: 600;
  }
  .blast-low { background: var(--c-warn-bg); color: var(--c-warn); }
  .blast-med { background: var(--c-warn-bg); color: var(--c-warn); border: 1px solid color-mix(in srgb, var(--c-warn) 25%, transparent); }
  .blast-high { background: var(--c-err-bg); color: var(--c-err); border: 1px solid color-mix(in srgb, var(--c-err) 25%, transparent); }

  /* ── Expandable rows ── */
  .expand-icon {
    display: inline-block; width: 14px;
    font-weight: 600; color: var(--c-text-3);
    transition: transform 150ms ease;
    margin-right: 4px;
  }
  .expand-icon.expanded { transform: rotate(90deg); }

  .row-expanded { background: var(--c-surface-hover); }

  .expand-row td {
    padding: 0 !important;
    border-top: none !important;
  }
  .expand-panel {
    padding: var(--sp-3) var(--sp-4) var(--sp-3) var(--sp-6);
    margin-left: var(--sp-5);
    background: var(--c-surface-inset);
    border-top: 1px solid var(--c-border);
    border-left: 2px solid var(--c-accent);
    border-radius: 0 0 var(--radius-xs) var(--radius-xs);
    animation: slideDown 200ms ease;
  }
  .expand-table {
    width: 100%; border-collapse: collapse; min-width: 0;
  }
  .expand-table th {
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3);
    padding: var(--sp-2) var(--sp-3);
    text-align: left; border-bottom: 1px solid var(--c-border);
  }
  .expand-table td {
    padding: var(--sp-3) var(--sp-3);
    font-size: var(--text-sm);
    border-bottom: 1px solid var(--c-border);
  }
  .expand-table tbody tr:last-child td { border-bottom: none; }
  .expand-table a { font-weight: 600; text-decoration: none; }
  .expand-table a:hover { text-decoration: underline; }

  .skeleton-table { width: 100%; max-width: 600px; }
  .skeleton-row { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-3); }
  .skeleton-row .skeleton-line { height: 18px; border-radius: var(--radius-xs); }

  @media (max-width: 768px) {
    .controls-row { gap: var(--sp-2); }
    .filter-search { flex: 1; min-width: 0; }
    .filter-search input { width: 100%; }
  }
</style>
