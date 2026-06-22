<script>
  import { ownerUrl, serviceUrl } from '../lib/router.ts';
  import { aggregateByOwner, complianceClass, statusClass, ownerKey, sourceTooltip, complianceStatusClass } from '../lib/format.ts';
  import { getFilters, setFilter } from '../lib/filters.svelte.ts';
  import OwnersBarChart from '../components/OwnersBarChart.svelte';
  import SummaryBar from '../components/SummaryBar.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';
  import ComplianceScore from '../components/ComplianceScore.svelte';
  import SourceDot from '../components/SourceDot.svelte';
  import SortControls from '../components/SortControls.svelte';

  let { services = [], initialLoading = false } = $props();

  let sortBy = $state('services');
  let sortAsc = $state(false);
  let statusFilter = $state('all'); // all | warnings | non-compliant | compliant
  let expandedOwner = $state(null);

  // The owner name search uses the shared store's `search` key, so a query carries
  // over from the services list and into shareable links.
  const filters = $derived(getFilters());
  let nameFilter = $derived(filters.search);

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

<!-- Overall fleet metrics (computed across all services, not just owners). -->
{#if services.length > 0}
  <SummaryBar {services} />
{/if}

{#if allOwners.length === 0}
  <EmptyState
    title={initialLoading ? undefined : 'No ownership data'}
    message={initialLoading ? 'Loading owners…' : "Services don't have owner fields set. Add owner to your contracts."}
    loading={initialLoading}
  />
{:else}
  <!-- Controls -->
  <div class="controls-row">
    <SortControls {sortBy} {sortAsc} options={SORT_OPTIONS} onChange={setSort} />

    <span class="controls-sep"></span>

    {#if filterCounts.warnings > 0}
      <button type="button" class="chip" class:active={statusFilter === 'warnings'} onclick={() => toggleFilter('warnings')}>
        <span class="chip-dot" style="background:var(--c-warn)"></span>
        Warnings <span class="chip-count">{filterCounts.warnings}</span>
      </button>
    {/if}
    {#if filterCounts.nonCompliant > 0}
      <button type="button" class="chip" class:active={statusFilter === 'non-compliant'} onclick={() => toggleFilter('non-compliant')}>
        <span class="chip-dot" style="background:var(--c-err)"></span>
        Non-Compliant <span class="chip-count">{filterCounts.nonCompliant}</span>
      </button>
    {/if}
    {#if filterCounts.compliant > 0}
      <button type="button" class="chip" class:active={statusFilter === 'compliant'} onclick={() => toggleFilter('compliant')}>
        <span class="chip-dot" style="background:var(--c-ok)"></span>
        Fully Compliant <span class="chip-count">{filterCounts.compliant}</span>
      </button>
    {/if}

    <div class="filter-search">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="12" height="12"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
      <input type="text" placeholder="Filter owners…" value={nameFilter} oninput={(e) => setFilter('search', e.currentTarget.value)} aria-label="Filter by owner name" />
    </div>
  </div>

  {#if owners.length === 0}
    <EmptyState title="No matching owners" message="Try a different search or filter." />
  {:else}
    <!-- Owners service status bar chart -->
    {#if owners.length > 0}
      <div class="chart-panel fade-in-up">
        <div class="chart-title">Services by owner</div>
        <OwnersBarChart data={owners} />
      </div>
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
                <ComplianceScore score={row.compliancePercent} />
              </td>
              <td>
                {#if row.totalBlast > 0}
                  <span class="blast-badge" class:blast-low={row.totalBlast < 3} class:blast-med={row.totalBlast >= 3 && row.totalBlast < 5} class:blast-high={row.totalBlast >= 5}>{row.totalBlast}</span>
                {:else}
                  <span class="blast-badge blast-zero">0</span>
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
                            <td><StatusBadge status={svc.contractStatus} /></td>
                            <td>
                              <ComplianceScore score={svc.complianceScore} status={svc.complianceStatus} />
                            </td>
                            <td>
                              {#if (svc.blastRadius || 0) > 0}
                                <span class="blast-badge" class:blast-low={svc.blastRadius < 3} class:blast-med={svc.blastRadius >= 3 && svc.blastRadius < 5} class:blast-high={svc.blastRadius >= 5}>{svc.blastRadius}</span>
                              {:else}
                                <span class="blast-badge blast-zero">0</span>
                              {/if}
                            </td>
                            <td>
                              {#each (svc.sources || [svc.source]) as src}
                                <SourceDot source={src} align="right" />
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

  /* ── Chart panels ── */
  .chart-panel { margin-bottom: var(--sp-4); }
  .chart-title {
    font-size: var(--text-xs); font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); margin-bottom: var(--sp-2);
  }

  /* ── Controls ── */
  .controls-row {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-4); flex-wrap: wrap;
  }
  .controls-sep {
    width: 1px; height: 20px; background: var(--c-border); flex-shrink: 0;
  }
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
  table { width: 100%; }
  th, td { white-space: nowrap; }
  th:first-child, td:first-child { white-space: normal; }

  .owner-name {
    font-weight: 600;
    text-decoration: none;
    display: inline-block;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
  }
  .owner-name:hover { text-decoration: underline; }

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
  .blast-zero { background: var(--c-neutral-bg); color: var(--c-text-3); }

  /* ── Expandable rows ── */
  .expand-icon {
    display: inline-block; width: 14px;
    font-weight: 600; color: var(--c-text-3);
    transition: transform 150ms ease;
    margin-right: 4px;
  }
  .expand-icon.expanded { transform: rotate(90deg); }

  .row-expanded { background: var(--c-surface-hover); }

  /* Only the direct wrapper cell collapses its padding — using a descendant
     selector here would leak `padding: 0` into the nested expand-table's cells,
     making their rows tight and letting the hover accent bar overlap the text. */
  .expand-row > td {
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
    padding: var(--sp-2) var(--sp-2);
    text-align: left; border-bottom: 1px solid var(--c-border);
    white-space: nowrap;
  }
  .expand-table th:first-child { white-space: normal; }
  .expand-table td {
    padding: var(--sp-2) var(--sp-2);
    font-size: var(--text-sm);
    border-bottom: 1px solid var(--c-border);
    white-space: nowrap;
  }
  .expand-table td:first-child { white-space: normal; }
  .expand-table tbody tr:last-child td { border-bottom: none; }
  .expand-table a { font-weight: 600; text-decoration: none; }
  .expand-table a:hover { text-decoration: underline; }

  @media (max-width: 768px) {
    .controls-row { gap: var(--sp-2); }
    .filter-search { flex: 1; min-width: 0; }
    .filter-search input { width: 100%; }
  }
</style>
