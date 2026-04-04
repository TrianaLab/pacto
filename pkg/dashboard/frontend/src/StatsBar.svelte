<script>
  import { ownerMatchesFilter } from './lib/format.ts';

  let { services = [], statusFilter = $bindable('all'), sourceFilter = $bindable('all'), nameFilter = $bindable('') } = $props();

  // Pre-filter by name and source (but NOT status) so pill counts update dynamically
  let baseFiltered = $derived.by(() => {
    let list = services;
    if (nameFilter) {
      const q = nameFilter.toLowerCase();
      list = list.filter((s) => s.name.toLowerCase().includes(q) || ownerMatchesFilter(s.owner, q));
    }
    if (sourceFilter !== 'all') {
      list = list.filter((s) => (s.sources || [s.source]).includes(sourceFilter));
    }
    return list;
  });

  let stats = $derived.by(() => {
    const s = { total: baseFiltered.length, compliant: 0, warning: 0, nonCompliant: 0, unknown: 0, reference: 0 };
    for (const svc of baseFiltered) {
      if (svc.contractStatus === 'Compliant') s.compliant++;
      else if (svc.contractStatus === 'Warning') s.warning++;
      else if (svc.contractStatus === 'NonCompliant') s.nonCompliant++;
      else if (svc.contractStatus === 'Reference') s.reference++;
      else s.unknown++;
    }
    return s;
  });

  let highBlastCount = $derived(baseFiltered.filter(s => (s.blastRadius || 0) >= 3).length);

  let activeSources = $derived.by(() => {
    const sourceSet = new Set();
    for (const svc of services) {
      for (const src of (svc.sources || [svc.source])) {
        if (src) sourceSet.add(src);
      }
    }
    return [...sourceSet].sort();
  });

  // Visual distribution bar
  let barSegments = $derived.by(() => {
    if (stats.total === 0) return [];
    const segments = [];
    if (stats.compliant > 0) segments.push({ status: 'Compliant', label: 'Compliant', count: stats.compliant, color: 'var(--c-ok)', pct: (stats.compliant / stats.total * 100), tip: 'All contract checks pass' });
    if (stats.warning > 0) segments.push({ status: 'Warning', label: 'Warning', count: stats.warning, color: 'var(--c-warn)', pct: (stats.warning / stats.total * 100), tip: 'Some contract checks fail (warnings or errors)' });
    if (stats.nonCompliant > 0) segments.push({ status: 'NonCompliant', label: 'Non-Compliant', count: stats.nonCompliant, color: 'var(--c-err)', pct: (stats.nonCompliant / stats.total * 100), tip: 'The contract has validation errors' });
    if (stats.reference > 0) segments.push({ status: 'Reference', label: 'Reference', count: stats.reference, color: 'var(--c-info)', pct: (stats.reference / stats.total * 100), tip: 'Shared contract definition with no deployed workload' });
    if (stats.unknown > 0) segments.push({ status: 'Unknown', label: 'Unknown', count: stats.unknown, color: 'var(--c-neutral)', pct: (stats.unknown / stats.total * 100), tip: 'Contract status could not be determined' });
    return segments;
  });

  function toggleStatus(status) {
    statusFilter = statusFilter === status ? 'all' : status;
  }

  function toggleSource(src) {
    sourceFilter = sourceFilter === src ? 'all' : src;
  }
</script>

{#if stats.total > 0}
  <div class="stats-bar">
    <!-- Distribution bar -->
    <div class="dist-bar">
      {#each barSegments as seg}
        <button
          type="button"
          class="dist-segment"
          class:dimmed={statusFilter !== 'all' && statusFilter !== seg.status}
          style="width:{Math.max(seg.pct, 2)}%; background:{seg.color}"
          onclick={() => toggleStatus(seg.status)}
          data-tip="{seg.label}: {seg.tip} ({seg.count})"
          aria-label="Filter by {seg.label} ({seg.count})"
        ></button>
      {/each}
    </div>

    <!-- Status pills -->
    <div class="filter-row">
      <button type="button" class="filter-pill" class:active={statusFilter === 'all'} onclick={() => statusFilter = 'all'} data-tip="Show all services regardless of contract status">
        All <span class="filter-count">{stats.total}</span>
      </button>
      {#each barSegments as seg}
        <button type="button" class="filter-pill" class:active={statusFilter === seg.status} onclick={() => toggleStatus(seg.status)} data-tip={seg.tip}>
          <span class="filter-dot" style="background:{seg.color}"></span>
          {seg.label} <span class="filter-count">{seg.count}</span>
        </button>
      {/each}

      {#if activeSources.length > 1}
        <span class="filter-sep"></span>
        {#each activeSources as src}
          <button type="button" class="filter-pill filter-pill-source" class:active={sourceFilter === src} onclick={() => toggleSource(src)}>
            <span class="source-dot source-dot-{src}"></span>
            {src}
          </button>
        {/each}
      {/if}

      {#if highBlastCount > 0}
        <span class="filter-sep"></span>
        <span class="blast-summary" data-tip="{highBlastCount} service{highBlastCount !== 1 ? 's' : ''} with blast radius of 3 or more">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="12" height="12"><circle cx="12" cy="12" r="3"/><circle cx="12" cy="12" r="7" opacity="0.4"/><circle cx="12" cy="12" r="11" opacity="0.2"/></svg>
          {highBlastCount} high impact
        </span>
      {/if}
      <span class="filter-sep"></span>
      <div class="filter-search">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="12" height="12"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        <input type="text" placeholder="Filter by name..." bind:value={nameFilter} aria-label="Filter by service name" />
      </div>
    </div>
  </div>
{/if}

<style>
  .stats-bar { margin-bottom: var(--sp-5); }

  .dist-bar {
    display: flex; height: 8px; border-radius: 4px; overflow: hidden;
    margin-bottom: var(--sp-3); gap: 1px;
  }
  .dist-segment {
    border: none; padding: 0; cursor: pointer;
    transition: opacity var(--transition);
    min-width: 4px;
  }
  .dist-segment:hover { opacity: 0.8; }
  .dist-segment.dimmed { opacity: 0.25; }

  .filter-row {
    display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: center;
  }
  .filter-pill {
    display: inline-flex; align-items: center; gap: 5px;
    padding: 5px 12px; border-radius: 100px;
    border: 1px solid var(--c-border); background: var(--c-surface);
    font: inherit; font-size: var(--text-xs); color: var(--c-text-2);
    cursor: pointer; transition: all var(--transition);
    white-space: nowrap;
    min-height: 32px;
  }
  .filter-pill:hover { border-color: var(--c-text-3); color: var(--c-text); }
  .filter-pill.active { border-color: var(--c-accent); background: var(--c-accent-bg); color: var(--c-accent); }
  .filter-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
  .filter-count { font-weight: 600; }
  .filter-sep {
    width: 1px; height: 18px; background: var(--c-border); margin: 0 var(--sp-1);
  }
  .filter-pill-source { text-transform: uppercase; font-size: var(--text-xs); font-weight: 600; }
  .filter-search {
    display: inline-flex; align-items: center; gap: 5px;
    padding: 4px 10px; border-radius: 100px;
    border: 1px solid var(--c-border); background: var(--c-surface);
    transition: border-color var(--transition);
    min-height: 32px;
  }
  .filter-search:focus-within { border-color: var(--c-accent); }
  .filter-search svg { color: var(--c-text-3); flex-shrink: 0; }
  .filter-search input {
    border: none; background: none; outline: none;
    font: inherit; font-size: var(--text-xs); color: var(--c-text);
    width: 120px; padding: 2px 0;
  }
  .blast-summary {
    display: inline-flex; align-items: center; gap: 5px;
    font-size: var(--text-xs); font-weight: 600;
    color: var(--c-warn);
    white-space: nowrap;
  }
  .blast-summary svg { flex-shrink: 0; }
  .filter-search input::placeholder { color: var(--c-text-3); }

  /* ─── Mobile ─── */
  @media (max-width: 768px) {
    .filter-row { gap: var(--sp-1); }
    .filter-sep { display: none; }
    .filter-search { flex: 1; min-width: 0; }
    .filter-search input { width: 100%; }
  }
</style>
