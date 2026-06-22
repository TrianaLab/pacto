<script>
  import { serviceUrl, ownerUrl } from '../lib/router.ts';
  import {
    ownerKey,
    complianceClass,
    readinessBucket,
    readinessBucketLabel,
    readinessBucketClass,
    summarize,
    isUrlEvidence,
    checkStatusClass,
    checkStatusLabel,
    assessmentCountdownLabel,
  } from '../lib/format.ts';
  import { getFilters, setFilter } from '../lib/filters.svelte.ts';
  import { applyFilters } from '../lib/filters.ts';
  import FilterBar from '../components/FilterBar.svelte';
  import SummaryBar from '../components/SummaryBar.svelte';
  import CategoryBreakdownChart from '../components/CategoryBreakdownChart.svelte';
  import ReadinessDonut from '../components/ReadinessDonut.svelte';

  let { services = [], initialLoading = false } = $props();

  let sortBy = $state('score');
  let sortAsc = $state(false);
  let expandedService = $state(null);

  // The visible set is driven by the shared filter store (FilterBar writes to it).
  const filters = $derived(getFilters());
  let filtered = $derived(applyFilters(services, filters));

  // Metrics over the FILTERED set, so the summary + category breakdown react.
  const metrics = $derived(summarize(filtered));
  const summary = $derived(metrics.readiness);
  const byCategory = $derived(metrics.byCategory);

  // Decorate each visible service with derived readiness fields used by the table.
  const decorated = $derived.by(() =>
    filtered.map((svc) => {
      const r = svc.readiness || null;
      return {
        svc,
        name: svc.name,
        owner: svc.owner,
        ownerName: ownerKey(svc.owner) || '(unowned)',
        bucket: readinessBucket(svc),
        r,
        score: r ? r.score : -1,
        done: r ? r.doneCount || 0 : 0,
        total: r ? r.checks?.length ?? 0 : 0,
        countdown: r ? assessmentCountdownLabel(r.expired, r.daysRemaining) : '',
        expired: r ? !!r.expired : false,
      };
    }),
  );

  const rows = $derived.by(() => {
    const dir = sortAsc ? 1 : -1;
    return [...decorated].sort((a, b) => {
      if (sortBy === 'name') return a.name.localeCompare(b.name) * dir;
      if (sortBy === 'owner') return a.ownerName.localeCompare(b.ownerName) * dir;
      if (sortBy === 'done') return (a.done - b.done) * dir;
      return (a.score - b.score) * dir; // 'score'
    });
  });

  function setSort(col) {
    if (sortBy === col) sortAsc = !sortAsc;
    else {
      sortBy = col;
      sortAsc = col === 'name' || col === 'owner';
    }
  }

  function sortIcon(col) {
    if (sortBy !== col) return '';
    return sortAsc ? ' ↑' : ' ↓';
  }

  function toggleExpand(name) {
    expandedService = expandedService === name ? null : name;
  }

  // Weight contribution as a % of the service's total declared weight.
  function pct(weight, total) {
    return total > 0 ? Math.round((weight * 100) / total) : 0;
  }

  const SORT_OPTIONS = [
    { value: 'score', label: 'Score' },
    { value: 'done', label: 'Done checks' },
    { value: 'owner', label: 'Owner' },
    { value: 'name', label: 'Name' },
  ];
</script>

<div class="page-header">
  <a href="#/" class="btn btn-sm btn-ghost">← Services</a>
  <h1>Service Readiness</h1>
  <span class="tab-count">{services.length}</span>
</div>

{#if services.length === 0}
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
      <p style="margin-top:var(--sp-3); color:var(--c-text-3)">Loading readiness…</p>
    {:else}
      <h3>No services</h3>
      <p>No services were found from the active sources.</p>
    {/if}
  </div>
{:else}
  <!-- Shared filter + summary: both react to the active filters. -->
  <FilterBar {services} />
  <SummaryBar services={filtered} />

  {#if summary.configured === 0}
    <p class="empty-hint">
      No service declares a <code>readiness</code> block yet — all are shown as <em>Not configured</em>.
    </p>
  {/if}

  <!-- Readiness charts: side-by-side in a responsive row -->
  {#if summary.configured > 0 || byCategory.length > 0}
    <div class="chart-row fade-in-up">
      {#if summary.configured > 0}
        <div class="chart-panel">
          <div class="chart-title">Status Distribution</div>
          <ReadinessDonut data={{ ready: summary.ready, partial: summary.partial, notReady: summary.notReady, notConfigured: summary.notConfigured }} />
        </div>
      {/if}
      {#if byCategory.length > 0}
        <div class="chart-panel">
          <div class="chart-title">By category</div>
          <CategoryBreakdownChart data={byCategory} />
        </div>
      {/if}
    </div>
  {/if}

  <!-- Sort controls -->
  <div class="controls-row">
    <div class="controls-left">
      <span class="control-label">Sort</span>
      {#each SORT_OPTIONS as opt}
        <button type="button" class="sort-chip" class:active={sortBy === opt.value} onclick={() => setSort(opt.value)}>
          {opt.label}{#if sortBy === opt.value}<span class="sort-arrow">{sortAsc ? '↑' : '↓'}</span>{/if}
        </button>
      {/each}
    </div>
  </div>

  {#if rows.length === 0}
    <div class="state-box">
      <h3>No matching services</h3>
      <p>Try a different search or filter.</p>
    </div>
  {:else}
    <div class="table-wrap fade-in-up">
      <table>
        <thead>
          <tr>
            <th><button type="button" class="col-sort" onclick={() => setSort('name')}>Service{sortIcon('name')}</button></th>
            <th><button type="button" class="col-sort" onclick={() => setSort('owner')}>Owner{sortIcon('owner')}</button></th>
            <th><button type="button" class="col-sort" data-tip="Derived readiness score (0–100%)" onclick={() => setSort('score')}>Score{sortIcon('score')}</button></th>
            <th data-tip="Ready = gate met · Partial = score ≥ 50% · Not Ready = score < 50% · Not configured = no readiness block">Status</th>
            <th data-tip="Checks marked done / total declared checks"><button type="button" class="col-sort" onclick={() => setSort('done')}>Checks{sortIcon('done')}</button></th>
            <th data-tip="When the overall assessment expires">Expiry</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as row (row.name)}
            <tr class="clickable" class:row-expanded={expandedService === row.name} onclick={() => toggleExpand(row.name)}>
              <td>
                <span class="expand-icon" class:expanded={expandedService === row.name}>›</span>
                <a href={serviceUrl(row.name)} class="service-name" onclick={(e) => e.stopPropagation()}>{row.name}</a>
              </td>
              <td>
                {#if row.ownerName === '(unowned)'}
                  <span class="text-dim">{row.ownerName}</span>
                {:else}
                  <a href={ownerUrl(row.ownerName)} onclick={(e) => { e.stopPropagation(); setFilter('owner', row.ownerName); }}>{row.ownerName}</a>
                {/if}
              </td>
              <td>
                {#if row.score >= 0}
                  <span class="score {complianceClass(row.score)}">{row.score}<span class="score-unit">%</span></span>
                {:else}
                  <span class="text-dim">—</span>
                {/if}
              </td>
              <td>
                <button type="button" class="badge-btn" onclick={(e) => { e.stopPropagation(); setFilter('readinessStatus', row.bucket); }}>
                  <span class="badge {readinessBucketClass(row.bucket)}"><span class="badge-dot"></span>{readinessBucketLabel(row.bucket)}</span>
                </button>
              </td>
              <td>
                {#if row.r}
                  <span class:text-ok={row.done === row.total && row.total > 0}>{row.done}</span><span class="text-dim">/{row.total}</span>
                {:else}
                  <span class="text-dim">—</span>
                {/if}
              </td>
              <td>
                {#if row.countdown}
                  <span class:text-err={row.expired}>{row.countdown}</span>
                {:else}
                  <span class="text-dim">—</span>
                {/if}
              </td>
            </tr>
            {#if expandedService === row.name}
              <tr class="expand-row">
                <td colspan="6">
                  <div class="expand-panel">
                    {#if row.r && (row.r.checks?.length ?? 0) > 0}
                      <table class="expand-table">
                        <thead>
                          <tr>
                            <th>Check</th>
                            <th>Type</th>
                            <th>Category</th>
                            <th>Status</th>
                            <th>Weight</th>
                            <th>Earned</th>
                            <th>Evidence</th>
                          </tr>
                        </thead>
                        <tbody>
                          {#each row.r.checks as c}
                            <tr>
                              <td>
                                <span class="check-id">{c.id}</span>
                                {#if c.description}<div class="check-desc">{c.description}</div>{/if}
                              </td>
                              <td><span class="pill">{c.type}</span></td>
                              <td>
                                {#if c.category}
                                  <button type="button" class="cat-name" onclick={(e) => { e.stopPropagation(); setFilter('category', c.category); }}>{c.category}</button>
                                {:else}
                                  <span class="text-dim">—</span>
                                {/if}
                              </td>
                              <td><span class="badge {checkStatusClass(c.status)}">{checkStatusLabel(c.status)}</span></td>
                              <td>{c.weight} <span class="text-dim">({pct(c.weight, row.r.totalWeight)}%)</span></td>
                              <td class="text-dim">{c.earnedWeight}</td>
                              <td class="evidence-cell">
                                {#if c.evidence}
                                  {#if isUrlEvidence(c.evidence)}
                                    <a href={c.evidence} target="_blank" rel="noopener noreferrer" onclick={(e) => e.stopPropagation()}>{c.evidence}</a>
                                  {:else}
                                    <code>{c.evidence}</code>
                                  {/if}
                                {:else}
                                  <span class="text-dim">No evidence</span>
                                {/if}
                              </td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    {:else}
                      <p class="no-checks">
                        No readiness declared. Add a <code>readiness</code> block to
                        <a href={serviceUrl(row.name)} onclick={(e) => e.stopPropagation()}>{row.name}</a>’s contract.
                      </p>
                    {/if}
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

  .empty-hint {
    font-size: var(--text-sm); color: var(--c-text-3);
    margin: 0 0 var(--sp-4);
  }

  /* ── Chart panels ── */
  .chart-row {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
    gap: var(--sp-4);
    margin-bottom: var(--sp-4);
  }
  .chart-panel { /* now inside chart-row */ }
  .chart-title {
    font-size: var(--text-xs); font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); margin-bottom: var(--sp-2);
  }
  .cat-name {
    background: none; border: none; padding: 0; font: inherit; font-weight: 600;
    color: var(--c-accent); cursor: pointer;
  }
  .cat-name:hover { text-decoration: underline; }

  /* ── Controls ── */
  .controls-row {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-3); flex-wrap: wrap;
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

  /* ── Table ── */
  table { width: 100%; }
  th, td { white-space: nowrap; }
  th:first-child, td:first-child { white-space: normal; }

  .badge-btn { background: none; border: none; padding: 0; font: inherit; cursor: pointer; }
  .service-name {
    font-weight: 600;
    text-decoration: none;
    display: inline-block;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
  }
  .service-name:hover { text-decoration: underline; }
  .score-unit { font-size: 0.8em; font-weight: 500; color: var(--c-text-3); margin-left: 1px; }
  .col-sort {
    background: none; border: none; padding: 0; font: inherit;
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); cursor: pointer; white-space: nowrap;
  }
  .col-sort:hover { color: var(--c-text); }

  .score { font-weight: 600; }
  .score.score-ok { color: var(--c-ok); }
  .score.score-warn { color: var(--c-warn); }
  .score.score-err { color: var(--c-err); }
  .text-dim { color: var(--c-text-3); }
  .text-ok { color: var(--c-ok); }
  .text-warn { color: var(--c-warn); }
  .text-err { color: var(--c-err); }

  /* ── Expandable rows ── */
  .expand-icon {
    display: inline-block; width: 14px; font-weight: 600; color: var(--c-text-3);
    transition: transform 150ms ease; margin-right: 4px;
  }
  .expand-icon.expanded { transform: rotate(90deg); }
  .row-expanded { background: var(--c-surface-hover); }
  /* Child combinator: collapse only the direct wrapper cell, never the nested
     expand-table's cells (a descendant selector would zero their padding and
     make rows tight + overlap the hover accent bar). */
  .expand-row > td { padding: 0 !important; border-top: none !important; }
  .expand-panel {
    padding: var(--sp-3) var(--sp-4) var(--sp-3) var(--sp-6);
    margin-left: var(--sp-5);
    background: var(--c-surface-inset);
    border-top: 1px solid var(--c-border);
    border-left: 2px solid var(--c-accent);
    border-radius: 0 0 var(--radius-xs) var(--radius-xs);
    animation: slideDown 200ms ease;
  }
  .expand-table { width: 100%; border-collapse: collapse; min-width: 0; table-layout: fixed; }
  .expand-table th {
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3);
    padding: var(--sp-2) var(--sp-2); text-align: left; border-bottom: 1px solid var(--c-border);
    white-space: nowrap;
  }
  .expand-table th:nth-child(1) { width: 25%; white-space: normal; }
  .expand-table th:nth-child(2) { width: 8%; }
  .expand-table th:nth-child(3) { width: 12%; }
  .expand-table th:nth-child(4) { width: 10%; }
  .expand-table th:nth-child(5) { width: 10%; }
  .expand-table th:nth-child(6) { width: 8%; }
  .expand-table th:nth-child(7) { width: 27%; white-space: normal; }
  .expand-table td {
    padding: var(--sp-2) var(--sp-2); font-size: var(--text-sm); border-bottom: 1px solid var(--c-border);
    vertical-align: top;
  }
  .expand-table tbody tr:last-child td { border-bottom: none; }
  .expand-table a { font-weight: 600; text-decoration: none; }
  .expand-table a:hover { text-decoration: underline; }
  .check-id { font-weight: 600; }
  .check-desc { font-size: var(--text-xs); color: var(--c-text-3); margin-top: 2px; }
  .evidence-cell { max-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .evidence-cell a, .evidence-cell code { font-size: var(--text-xs); }
  .evidence-cell code { color: var(--c-text-2); }
  .no-checks { font-size: var(--text-sm); color: var(--c-text-3); margin: 0; padding: var(--sp-2) 0; }

  .skeleton-table { width: 100%; max-width: 600px; }
  .skeleton-row { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-3); }
  .skeleton-row .skeleton-line { height: 18px; border-radius: var(--radius-xs); }

  @media (max-width: 768px) {
    .controls-row { gap: var(--sp-2); }
  }
</style>
