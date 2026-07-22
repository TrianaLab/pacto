<script>
  import { serviceUrl, ownerUrl } from '../lib/router.ts';
  import {
    ownerKey,
    readinessBucket,
    readinessBucketLabel,
    readinessBucketClass,
    summarize,
    isUrlEvidence,
    checkStatusClass,
    checkStatusLabel,
    assessmentCountdownLabel,
    compareScoresUnassessedLast,
  } from '../lib/format.ts';
  import { getFilters, setFilter } from '../lib/filters.svelte.ts';
  import { applyFilters } from '../lib/filters.ts';
  import FilterBar from '../components/FilterBar.svelte';
  import SummaryBar from '../components/SummaryBar.svelte';
  import CategoryBreakdownChart from '../components/CategoryBreakdownChart.svelte';
  import ReadinessDonut from '../components/ReadinessDonut.svelte';
  import PriorityQuadrant from '../components/PriorityQuadrant.svelte';
  import ReadinessHeatmap from '../components/ReadinessHeatmap.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ReadinessScore from '../components/ReadinessScore.svelte';
  import SortControls from '../components/SortControls.svelte';
  import { quadrantData, heatmapData } from '../lib/chartData.ts';

  let { services = [], initialLoading = false } = $props();

  let sortBy = $state('score');
  let sortAsc = $state(false);
  let expandedService = $state(null);
  let breakdown = $state('category');

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
      return compareScoresUnassessedLast(a.score, b.score, dir); // 'score' (-1 = not configured, always last)
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
  <EmptyState
    title={initialLoading ? undefined : 'No services'}
    message={initialLoading ? 'Loading readiness…' : 'No services were found from the active sources.'}
    loading={initialLoading}
  />
{:else}
  <!-- Shared filter + summary: both react to the active filters. -->
  <FilterBar {services} />
  <SummaryBar services={filtered} />

  {#if summary.configured === 0}
    <p class="empty-hint">
      No service declares a <code>readiness</code> block yet — all are shown as <em>Not configured</em>.
    </p>
  {:else}
    <div class="charts-container fade-in-up">
      <!-- Focal pair: Where we stand + What to fix first -->
      <div class="focal-pair">
        <div class="chart-panel">
          <div class="chart-title">Where we stand</div>
          <ReadinessDonut data={{ ready: summary.ready, partial: summary.partial, notReady: summary.notReady, notConfigured: summary.notConfigured }} />
        </div>
        <div class="chart-panel">
          <div class="chart-title">What to fix first</div>
          <PriorityQuadrant data={quadrantData(filtered)} onSelect={(name) => location.hash = serviceUrl(name)} />
        </div>
      </div>

      <!-- Breakdown toggle: By category / By team / None -->
      <div class="breakdown-controls">
        <div class="seg" role="group" aria-label="Breakdown view">
          <button type="button" class="seg-btn" class:active={breakdown === 'category'} aria-pressed={breakdown === 'category'} onclick={() => breakdown = 'category'}>By category</button>
          <button type="button" class="seg-btn" class:active={breakdown === 'team'} aria-pressed={breakdown === 'team'} onclick={() => breakdown = 'team'}>By team</button>
          <button type="button" class="seg-btn" class:active={breakdown === 'none'} aria-pressed={breakdown === 'none'} onclick={() => breakdown = 'none'}>None</button>
        </div>
      </div>

      <!-- Breakdown region driven by toggle -->
      {#if breakdown === 'category' && byCategory.length > 0}
        <div class="chart-panel">
          <CategoryBreakdownChart data={byCategory} />
        </div>
      {:else if breakdown === 'team'}
        <div class="chart-panel">
          <ReadinessHeatmap data={heatmapData(filtered)} />
        </div>
      {/if}
    </div>
  {/if}

  <!-- Sort controls -->
  <div class="controls-row">
    <SortControls {sortBy} {sortAsc} options={SORT_OPTIONS} onChange={setSort} />
  </div>

  {#if rows.length === 0}
    <EmptyState title="No matching services" message="Try a different search or filter." />
  {:else}
    <p class="readiness-legend">
      <span class="rl-key"><span class="rl-swatch score-ok"></span>Ready = gate met</span>
      <span class="rl-key"><span class="rl-swatch score-warn"></span>Partial = score ≥ 50%</span>
      <span class="rl-key"><span class="rl-swatch score-err"></span>Not Ready = score &lt; 50%</span>
      <span class="rl-key"><span class="rl-swatch score-none"></span>Not configured = no readiness block</span>
    </p>
    <div class="table-wrap fade-in-up">
      <table class="readiness-list">
        <colgroup>
          <col class="rl-service" />
          <col class="rl-owner" />
          <col class="rl-score" />
          <col class="rl-status" />
          <col class="rl-checks" />
          <col class="rl-expiry" />
        </colgroup>
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
          {#each rows as row, i (row.name)}
            <tr class="clickable" class:row-expanded={expandedService === row.name} onclick={() => toggleExpand(row.name)}>
              <td>
                <button type="button" class="expand-icon" class:expanded={expandedService === row.name}
                  aria-expanded={expandedService === row.name} aria-controls="readiness-detail-{i}"
                  aria-label="Toggle checks for {row.name}"
                  onclick={(e) => { e.stopPropagation(); toggleExpand(row.name); }}>›</button>
                <a href={serviceUrl(row.name)} class="service-name" onclick={(e) => e.stopPropagation()}>{row.name}</a>
              </td>
              <td>
                {#if row.ownerName === '(unowned)'}
                  <span class="text-dim owner-name">{row.ownerName}</span>
                {:else}
                  <a class="owner-name" href={ownerUrl(row.ownerName)} onclick={(e) => { e.stopPropagation(); setFilter('owner', row.ownerName); }}>{row.ownerName}</a>
                {/if}
              </td>
              <td>
                {#if row.r}
                  <ReadinessScore readiness={row.r} />
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
              <tr class="expand-row" id="readiness-detail-{i}">
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
  .charts-container {
    max-width: 900px;
    margin: 0 auto var(--sp-4);
  }

  .focal-pair {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: var(--sp-4);
    margin-bottom: var(--sp-4);
  }

  .chart-panel { /* now inside focal-pair or breakdown region */ }

  .chart-title {
    font-size: var(--text-xs); font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); margin-bottom: var(--sp-2);
  }

  .breakdown-controls {
    display: flex;
    justify-content: center;
    margin-bottom: var(--sp-4);
  }

  .seg {
    display: inline-flex;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .seg-btn {
    padding: 4px 10px;
    font-size: var(--text-xs);
    background: var(--c-surface);
    color: var(--c-text-3);
    border: 0;
    cursor: pointer;
  }

  .seg-btn.active {
    background: var(--c-accent);
    color: #fff;
  }
  .cat-name {
    background: none; border: none; padding: 0; font: inherit; font-weight: 600;
    color: var(--c-accent); cursor: pointer;
    /* Wrap long category names so they stay inside the fixed column rather than
       bleeding into Status (the cell inherits nowrap from the th,td rule). */
    white-space: normal; overflow-wrap: anywhere; text-align: left;
  }
  .cat-name:hover { text-decoration: underline; }

  /* ── Controls ── */
  .controls-row {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-3); flex-wrap: wrap;
  }

  /* ── Table ── */
  table { width: 100%; }
  th, td { white-space: nowrap; }
  th:first-child, td:first-child { white-space: normal; }

  /* Fixed layout sizes columns from <colgroup>, not content min-content, so the
     nowrap cells never over-grow the table and flash a spurious horizontal
     scrollbar. The Service column is left flexible to absorb %-rounding. Scoped
     to .readiness-list so the nested .expand-table keeps its own widths. */
  .readiness-list { table-layout: fixed; }
  .readiness-list th { white-space: normal; }
  /* Direct rows only: overflow:visible lets outer-cell tooltips escape, but must
     NOT cascade into the nested .expand-table — it would defeat .evidence-cell's
     overflow:hidden ellipsis and spill evidence text past the table edge. */
  .readiness-list > tbody > tr > td { overflow: visible; }
  .rl-service { width: auto; }
  .rl-owner { width: 18%; }
  .rl-score { width: 11%; }
  .rl-status { width: 16%; }
  .rl-checks { width: 11%; }
  .rl-expiry { width: 16%; }

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
  /* Truncate long owner names so they can't bleed into the Score column. */
  .owner-name {
    display: inline-block; max-width: 100%; vertical-align: middle;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .text-dim { color: var(--c-text-3); }
  .text-ok { color: var(--c-ok); }
  .text-warn { color: var(--c-warn); }
  .text-err { color: var(--c-err); }

  .readiness-legend {
    display: flex; flex-wrap: wrap; gap: var(--sp-2) var(--sp-4);
    margin-bottom: var(--sp-3);
    font-size: var(--text-xs); color: var(--c-text-3);
  }
  .rl-key { display: inline-flex; align-items: center; gap: 6px; }
  .rl-swatch { width: 9px; height: 9px; border-radius: 2px; flex-shrink: 0; }
  .rl-swatch.score-ok { background: var(--c-ok); }
  .rl-swatch.score-warn { background: var(--c-warn); }
  .rl-swatch.score-err { background: var(--c-err); }
  .rl-swatch.score-none { background: var(--c-neutral); }

  /* ── Expandable rows ── */
  .expand-icon {
    display: inline-block; width: 14px; font-weight: 600; color: var(--c-text-3);
    transition: transform 150ms ease; margin-right: 4px;
    background: none; border: none; padding: 0; cursor: pointer;
    font-size: inherit; line-height: 1;
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

  @media (max-width: 768px) {
    .controls-row { gap: var(--sp-2); }
  }
</style>
