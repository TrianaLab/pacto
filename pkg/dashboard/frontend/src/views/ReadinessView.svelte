<script>
  import { serviceUrl, ownerUrl } from '../lib/router.ts';
  import {
    ownerKey,
    ownerMatchesFilter,
    complianceClass,
    readinessBucket,
    readinessBucketLabel,
    readinessBucketClass,
    summarizeReadiness,
    readinessCheckTypes,
    isUrlEvidence,
    readinessStatusClass,
    readinessDaysLabel,
  } from '../lib/format.ts';

  let { services = [], initialLoading = false } = $props();

  let sortBy = $state('score');
  let sortAsc = $state(false);
  let nameFilter = $state('');
  let bucketFilter = $state('all'); // all | ready | partial | not-ready | unknown
  let ownerFilter = $state('all');
  let typeFilter = $state('all'); // evidence kind
  let checkStatusFilter = $state('all'); // Current | Expired | Invalid
  let expandedService = $state(null);

  const summary = $derived(summarizeReadiness(services));

  // Decorate each service with derived readiness fields used by the table.
  const decorated = $derived.by(() =>
    services.map((svc) => {
      const r = svc.readiness || null;
      return {
        svc,
        name: svc.name,
        owner: svc.owner,
        ownerName: ownerKey(svc.owner) || '(unowned)',
        bucket: readinessBucket(svc),
        r,
        score: r ? r.score : -1,
        current: r ? r.currentCount || 0 : 0,
        total: r ? r.checks?.length ?? 0 : 0,
        expired: r ? r.expiredCount || 0 : 0,
        invalid: r ? r.invalidCount || 0 : 0,
      };
    }),
  );

  const ownerOptions = $derived.by(() => {
    const set = new Set();
    for (const d of decorated) set.add(d.ownerName);
    return Array.from(set).sort((a, b) => a.localeCompare(b));
  });

  const typeOptions = $derived(readinessCheckTypes(services));

  const checkStatusOptions = $derived.by(() => {
    const set = new Set();
    for (const d of decorated) for (const c of d.r?.checks ?? []) if (c.status) set.add(c.status);
    return Array.from(set).sort();
  });

  const rows = $derived.by(() => {
    let list = decorated;

    if (nameFilter) {
      const q = nameFilter.toLowerCase();
      list = list.filter((d) => d.name.toLowerCase().includes(q) || ownerMatchesFilter(d.owner, q));
    }
    if (ownerFilter !== 'all') list = list.filter((d) => d.ownerName === ownerFilter);
    if (bucketFilter !== 'all') list = list.filter((d) => d.bucket === bucketFilter);
    if (typeFilter !== 'all') list = list.filter((d) => (d.r?.checks ?? []).some((c) => c.type === typeFilter));
    if (checkStatusFilter !== 'all') list = list.filter((d) => (d.r?.checks ?? []).some((c) => c.status === checkStatusFilter));

    const dir = sortAsc ? 1 : -1;
    return [...list].sort((a, b) => {
      if (sortBy === 'name') return a.name.localeCompare(b.name) * dir;
      if (sortBy === 'owner') return a.ownerName.localeCompare(b.ownerName) * dir;
      if (sortBy === 'expired') return (a.expired - b.expired) * dir;
      if (sortBy === 'invalid') return (a.invalid - b.invalid) * dir;
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

  function toggleBucket(b) {
    bucketFilter = bucketFilter === b ? 'all' : b;
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
    { value: 'expired', label: 'Expired' },
    { value: 'invalid', label: 'Invalid' },
    { value: 'owner', label: 'Owner' },
    { value: 'name', label: 'Name' },
  ];

  const BUCKET_CHIPS = [
    { value: 'ready', label: 'Ready', color: 'var(--c-ok)', key: 'ready' },
    { value: 'partial', label: 'Partial', color: 'var(--c-warn)', key: 'partial' },
    { value: 'not-ready', label: 'Not Ready', color: 'var(--c-err)', key: 'notReady' },
    { value: 'unknown', label: 'Not configured', color: 'var(--c-neutral)', key: 'notConfigured' },
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
  <!-- Global summary -->
  <div class="summary-cards fade-in-up">
    <div class="summary-card">
      <span class="summary-count">{summary.total}</span>
      <span class="summary-label">Services</span>
    </div>
    <div class="summary-card card-ok">
      <span class="summary-count">{summary.ready}</span>
      <span class="summary-label">Ready</span>
    </div>
    <div class="summary-card card-warn">
      <span class="summary-count">{summary.partial}</span>
      <span class="summary-label">Partial</span>
    </div>
    <div class="summary-card card-err">
      <span class="summary-count">{summary.notReady}</span>
      <span class="summary-label">Not Ready</span>
    </div>
    {#if summary.notConfigured > 0}
      <div class="summary-card card-neutral">
        <span class="summary-count">{summary.notConfigured}</span>
        <span class="summary-label">Not configured</span>
      </div>
    {/if}
    <div class="summary-card">
      {#if summary.avgScore >= 0}
        <span class="summary-count score {complianceClass(summary.avgScore)}">{summary.avgScore}</span>
      {:else}
        <span class="summary-count text-dim">—</span>
      {/if}
      <span class="summary-label">Avg score</span>
    </div>
    <div class="summary-card">
      <span class="summary-count" class:text-err={summary.totalExpired > 0}>{summary.totalExpired}</span>
      <span class="summary-label">Expired checks</span>
    </div>
    <div class="summary-card">
      <span class="summary-count" class:text-warn={summary.totalInvalid > 0}>{summary.totalInvalid}</span>
      <span class="summary-label">Invalid checks</span>
    </div>
  </div>

  {#if summary.configured === 0}
    <p class="empty-hint">
      No service declares a <code>readiness</code> block yet — all are shown as <em>Not configured</em>.
    </p>
  {/if}

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

      {#each BUCKET_CHIPS as chip}
        <button type="button" class="filter-chip" class:active={bucketFilter === chip.value} onclick={() => toggleBucket(chip.value)}>
          <span class="chip-dot" style="background:{chip.color}"></span>
          {chip.label} <span class="chip-count">{summary[chip.key]}</span>
        </button>
      {/each}
    </div>

    <div class="filter-search">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="12" height="12"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
      <input type="text" placeholder="Filter services…" bind:value={nameFilter} aria-label="Filter by service or owner name" />
    </div>
  </div>

  <!-- Secondary filters -->
  <div class="filter-row">
    <label class="filter-select">
      <span>Owner</span>
      <select bind:value={ownerFilter} aria-label="Filter by owner">
        <option value="all">All</option>
        {#each ownerOptions as o}<option value={o}>{o}</option>{/each}
      </select>
    </label>
    {#if typeOptions.length > 0}
      <label class="filter-select">
        <span>Check type</span>
        <select bind:value={typeFilter} aria-label="Filter by check type">
          <option value="all">All</option>
          {#each typeOptions as t}<option value={t}>{t}</option>{/each}
        </select>
      </label>
    {/if}
    {#if checkStatusOptions.length > 0}
      <label class="filter-select">
        <span>Check status</span>
        <select bind:value={checkStatusFilter} aria-label="Filter by check status">
          <option value="all">All</option>
          {#each checkStatusOptions as s}<option value={s}>{s}</option>{/each}
        </select>
      </label>
    {/if}
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
            <th><button type="button" class="col-sort" data-tip="Derived readiness score (0–100)" onclick={() => setSort('score')}>Score{sortIcon('score')}</button></th>
            <th>Status</th>
            <th data-tip="Current checks / total declared checks">Checks</th>
            <th data-tip="Checks whose evidence has expired"><button type="button" class="col-sort" onclick={() => setSort('expired')}>Expired{sortIcon('expired')}</button></th>
            <th data-tip="Checks with an unparseable expiry date"><button type="button" class="col-sort" onclick={() => setSort('invalid')}>Invalid{sortIcon('invalid')}</button></th>
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
                  <a href={ownerUrl(row.ownerName)} onclick={(e) => e.stopPropagation()}>{row.ownerName}</a>
                {/if}
              </td>
              <td>
                {#if row.score >= 0}
                  <span class="score {complianceClass(row.score)}">{row.score}</span>
                {:else}
                  <span class="text-dim">—</span>
                {/if}
              </td>
              <td><span class="badge {readinessBucketClass(row.bucket)}"><span class="badge-dot"></span>{readinessBucketLabel(row.bucket)}</span></td>
              <td>
                {#if row.r}
                  <span class:text-ok={row.current === row.total && row.total > 0}>{row.current}</span><span class="text-dim">/{row.total}</span>
                {:else}
                  <span class="text-dim">—</span>
                {/if}
              </td>
              <td>{#if row.expired > 0}<span class="text-err">{row.expired}</span>{:else}<span class="text-dim">0</span>{/if}</td>
              <td>{#if row.invalid > 0}<span class="text-warn">{row.invalid}</span>{:else}<span class="text-dim">0</span>{/if}</td>
            </tr>
            {#if expandedService === row.name}
              <tr class="expand-row">
                <td colspan="7">
                  <div class="expand-panel">
                    {#if row.r && (row.r.checks?.length ?? 0) > 0}
                      <table class="expand-table">
                        <thead>
                          <tr>
                            <th>Check</th>
                            <th>Type</th>
                            <th>Status</th>
                            <th>Weight</th>
                            <th>Expires</th>
                            <th>Remaining</th>
                            <th>Evidence</th>
                          </tr>
                        </thead>
                        <tbody>
                          {#each row.r.checks as c}
                            <tr class:check-stale={c.status !== 'Current'}>
                              <td>
                                <span class="check-id">{c.id}</span>
                                {#if c.description}<div class="check-desc">{c.description}</div>{/if}
                              </td>
                              <td><span class="pill">{c.type}</span></td>
                              <td><span class="badge {readinessStatusClass(c.status)}">{c.status}</span></td>
                              <td>{c.weight} <span class="text-dim">({pct(c.weight, row.r.totalWeight)}%)</span></td>
                              <td><code class:date-expired={c.status === 'Expired'}>{c.expires}</code></td>
                              <td class="text-dim">{readinessDaysLabel(c.status, c.daysRemaining)}</td>
                              <td class="evidence-cell">
                                {#if c.evidence}
                                  {#if isUrlEvidence(c.evidence)}
                                    <a href={c.evidence} target="_blank" rel="noopener noreferrer">{c.evidence}</a>
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

  /* ── Summary cards ── */
  .summary-cards {
    display: flex; gap: var(--sp-3); margin-bottom: var(--sp-4); flex-wrap: wrap;
  }
  .summary-card {
    display: flex; flex-direction: column; align-items: center;
    padding: var(--sp-3) var(--sp-4);
    border-radius: var(--radius-sm);
    background: var(--c-surface); border: 1px solid var(--c-border);
    min-width: 80px;
  }
  .summary-count { font-size: 1.25rem; font-weight: 700; }
  .summary-label { font-size: var(--text-xs); color: var(--c-text-3); margin-top: 2px; }
  .card-ok { border-color: var(--c-ok-border); }
  .card-ok .summary-count { color: var(--c-ok); }
  .card-warn { border-color: var(--c-warn-border); }
  .card-warn .summary-count { color: var(--c-warn); }
  .card-err { border-color: var(--c-err-border); }
  .card-err .summary-count { color: var(--c-err); }
  .card-neutral .summary-count { color: var(--c-text-3); }

  .empty-hint {
    font-size: var(--text-sm); color: var(--c-text-3);
    margin: 0 0 var(--sp-4);
  }

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
  .controls-sep { width: 1px; height: 20px; background: var(--c-border); flex-shrink: 0; }

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
    transition: border-color var(--transition); min-height: 30px;
  }
  .filter-search:focus-within { border-color: var(--c-accent); }
  .filter-search svg { color: var(--c-text-3); flex-shrink: 0; }
  .filter-search input {
    border: none; background: none; outline: none;
    font: inherit; font-size: var(--text-xs); color: var(--c-text);
    width: 130px; padding: 2px 0;
  }
  .filter-search input::placeholder { color: var(--c-text-3); }

  .filter-row {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-4); flex-wrap: wrap;
  }
  .filter-select {
    display: inline-flex; align-items: center; gap: 6px;
    font-size: var(--text-xs); color: var(--c-text-3);
  }
  .filter-select select {
    font: inherit; font-size: var(--text-xs); color: var(--c-text);
    background: var(--c-surface); border: 1px solid var(--c-border);
    border-radius: var(--radius-xs); padding: 4px 8px; min-height: 30px;
    cursor: pointer;
  }
  .filter-select select:focus { outline: none; border-color: var(--c-accent); }

  /* ── Table ── */
  .service-name { font-weight: 600; text-decoration: none; }
  .service-name:hover { text-decoration: underline; }
  .col-sort {
    background: none; border: none; padding: 0; font: inherit;
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); cursor: pointer; white-space: nowrap;
  }
  .col-sort:hover { color: var(--c-text); }

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
  .expand-table { width: 100%; border-collapse: collapse; min-width: 0; }
  .expand-table th {
    font-size: var(--text-xs); font-weight: 500; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3);
    padding: var(--sp-2) var(--sp-3); text-align: left; border-bottom: 1px solid var(--c-border);
  }
  .expand-table td {
    padding: var(--sp-3) var(--sp-3); font-size: var(--text-sm); border-bottom: 1px solid var(--c-border);
    vertical-align: top;
  }
  .expand-table tbody tr:last-child td { border-bottom: none; }
  .expand-table a { font-weight: 600; text-decoration: none; }
  .expand-table a:hover { text-decoration: underline; }
  .check-id { font-weight: 600; }
  .check-desc { font-size: var(--text-xs); color: var(--c-text-3); margin-top: 2px; }
  .check-stale td { background: color-mix(in srgb, var(--c-err) 5%, transparent); }
  .date-expired { color: var(--c-err); }
  .evidence-cell a, .evidence-cell code { font-size: var(--text-xs); word-break: break-all; }
  .evidence-cell code { color: var(--c-text-2); }
  .no-checks { font-size: var(--text-sm); color: var(--c-text-3); margin: 0; padding: var(--sp-2) 0; }

  .skeleton-table { width: 100%; max-width: 600px; }
  .skeleton-row { display: flex; gap: var(--sp-3); margin-bottom: var(--sp-3); }
  .skeleton-row .skeleton-line { height: 18px; border-radius: var(--radius-xs); }

  @media (max-width: 768px) {
    .controls-row { gap: var(--sp-2); }
    .filter-search { flex: 1; min-width: 0; }
    .filter-search input { width: 100%; }
    .summary-cards { gap: var(--sp-2); }
    .summary-card { min-width: 64px; padding: var(--sp-2) var(--sp-3); }
  }
</style>
