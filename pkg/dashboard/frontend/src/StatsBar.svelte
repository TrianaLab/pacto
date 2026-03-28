<script>
  let { services = [], statusFilter = $bindable('all'), sourceFilter = $bindable('all'), nameFilter = $bindable('') } = $props();

  let stats = $derived.by(() => {
    const s = { total: services.length, compliant: 0, warning: 0, nonCompliant: 0, unknown: 0, reference: 0 };
    for (const svc of services) {
      if (svc.contractStatus === 'Compliant') s.compliant++;
      else if (svc.contractStatus === 'Warning') s.warning++;
      else if (svc.contractStatus === 'NonCompliant') s.nonCompliant++;
      else if (svc.contractStatus === 'Reference') s.reference++;
      else s.unknown++;
    }
    return s;
  });

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
    display: flex; height: 6px; border-radius: 3px; overflow: hidden;
    margin-bottom: var(--sp-2); gap: 1px;
  }
  .dist-segment {
    border: none; padding: 0; cursor: pointer;
    transition: opacity var(--transition);
    min-width: 4px;
  }
  .dist-segment:hover { opacity: 0.8; }
  .dist-segment.dimmed { opacity: 0.25; }

  .filter-row {
    display: flex; gap: var(--sp-1); flex-wrap: wrap; align-items: center;
  }
  .filter-pill {
    display: inline-flex; align-items: center; gap: 4px;
    padding: 3px 10px; border-radius: 100px;
    border: 1px solid var(--c-border); background: var(--c-surface);
    font: inherit; font-size: var(--text-xs); color: var(--c-text-2);
    cursor: pointer; transition: all var(--transition);
    white-space: nowrap;
  }
  .filter-pill:hover { border-color: var(--c-text-3); color: var(--c-text); }
  .filter-pill.active { border-color: var(--c-accent); background: var(--c-accent-bg); color: var(--c-accent); }
  .filter-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
  .filter-count { font-weight: 600; }
  .filter-sep {
    width: 1px; height: 16px; background: var(--c-border); margin: 0 var(--sp-1);
  }
  .filter-pill-source { text-transform: uppercase; font-size: 10px; font-weight: 600; }
  .filter-search {
    display: inline-flex; align-items: center; gap: 4px;
    padding: 2px 8px; border-radius: 100px;
    border: 1px solid var(--c-border); background: var(--c-surface);
    transition: border-color var(--transition);
  }
  .filter-search:focus-within { border-color: var(--c-accent); }
  .filter-search svg { color: var(--c-text-3); flex-shrink: 0; }
  .filter-search input {
    border: none; background: none; outline: none;
    font: inherit; font-size: var(--text-xs); color: var(--c-text);
    width: 120px; padding: 1px 0;
  }
  .filter-search input::placeholder { color: var(--c-text-3); }
</style>
