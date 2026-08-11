<script>
  import { getFilters, setFilter, clearFilters } from '../lib/filters.svelte.ts';
  import { filtersActive } from '../lib/filters.ts';
  import { ownerKey, ownerKeyLabel, ownerKeyKind, UNOWNED_KEY, readinessBucket } from '../lib/format.ts';
  import Select from './Select.svelte';

  // The UNFILTERED list for computing facet counts
  let { services = [] } = $props();

  const filters = $derived(getFilters());
  const active = $derived(filtersActive(filters));

  // Facet counts from the unfiltered list
  const ownerCounts = $derived.by(() => {
    const counts = new Map();
    for (const svc of services) {
      const key = ownerKey(svc.owner) || UNOWNED_KEY;
      counts.set(key, (counts.get(key) || 0) + 1);
    }
    return counts;
  });

  const categoryCounts = $derived.by(() => {
    const counts = new Map();
    for (const svc of services) {
      const checks = svc.readiness?.checks || [];
      const categories = new Set();
      for (const c of checks) {
        const cat = c.category || 'other';
        categories.add(cat);
      }
      for (const cat of categories) {
        counts.set(cat, (counts.get(cat) || 0) + 1);
      }
    }
    return counts;
  });

  const readinessStatusCounts = $derived.by(() => {
    const counts = new Map();
    for (const svc of services) {
      const bucket = readinessBucket(svc);
      counts.set(bucket, (counts.get(bucket) || 0) + 1);
    }
    return counts;
  });

  const contractStatusCounts = $derived.by(() => {
    const counts = new Map();
    for (const svc of services) {
      const status = svc.contractStatus || 'Unknown';
      counts.set(status, (counts.get(status) || 0) + 1);
    }
    return counts;
  });

  const sourceCounts = $derived.by(() => {
    const counts = new Map();
    for (const svc of services) {
      const sources = svc.sources || (svc.source ? [svc.source] : []);
      for (const src of sources) {
        counts.set(src, (counts.get(src) || 0) + 1);
      }
    }
    return counts;
  });

  // Sorted by the name a reader scans for, not by the encoding, so `dri:z` does
  // not sit above `team:a`.
  const ownerOptions = $derived(Array.from(ownerCounts.keys()).sort((a, b) => ownerKeyLabel(a).localeCompare(ownerKeyLabel(b)) || a.localeCompare(b)));
  const categoryOptions = $derived(Array.from(categoryCounts.keys()).sort());
  const readinessStatusOptions = $derived(Array.from(readinessStatusCounts.keys()).sort());
  const contractStatusOptions = $derived(Array.from(contractStatusCounts.keys()).sort());
  const sourceOptions = $derived(Array.from(sourceCounts.keys()).sort());
</script>

<div class="filter-bar">
  <!-- Search input -->
  <div class="filter-search">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="12" height="12"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
    <input
      type="text"
      placeholder="Filter services…"
      value={filters.search}
      oninput={(e) => setFilter('search', e.target.value)}
      aria-label="Filter by service or owner name"
    />
  </div>

  <!-- Owner facet -->
  {#if ownerOptions.length > 0}
    <Select
      label="Owner"
      value={filters.owner}
      options={[{ value: '', label: 'All' }, ...ownerOptions.map(o => ({ value: o, label: `${ownerKeyLabel(o)}${ownerKeyKind(o) ? ` (${ownerKeyKind(o)})` : ''} (${ownerCounts.get(o)})` }))]}
      onchange={(e) => setFilter('owner', e.target.value)}
      ariaLabel="Filter by owner"
    />
  {/if}

  <!-- Category facet -->
  {#if categoryOptions.length > 0}
    <Select
      label="Category"
      value={filters.category}
      options={[{ value: '', label: 'All' }, ...categoryOptions.map(c => ({ value: c, label: `${c} (${categoryCounts.get(c)})` }))]}
      onchange={(e) => setFilter('category', e.target.value)}
      ariaLabel="Filter by readiness category"
    />
  {/if}

  <!-- Readiness status facet -->
  {#if readinessStatusOptions.length > 0}
    <Select
      label="Readiness"
      value={filters.readinessStatus}
      options={[{ value: '', label: 'All' }, ...readinessStatusOptions.map(r => ({ value: r, label: `${r} (${readinessStatusCounts.get(r)})` }))]}
      onchange={(e) => setFilter('readinessStatus', e.target.value)}
      ariaLabel="Filter by readiness status"
    />
  {/if}

  <!-- Contract status facet -->
  {#if contractStatusOptions.length > 0}
    <Select
      label="Contract Status"
      value={filters.contractStatus}
      options={[{ value: '', label: 'All' }, ...contractStatusOptions.map(s => ({ value: s, label: `${s} (${contractStatusCounts.get(s)})` }))]}
      onchange={(e) => setFilter('contractStatus', e.target.value)}
      ariaLabel="Filter by contract status"
    />
  {/if}

  <!-- Source facet -->
  {#if sourceOptions.length > 0}
    <Select
      label="Source"
      value={filters.source}
      options={[{ value: '', label: 'All' }, ...sourceOptions.map(s => ({ value: s, label: `${s} (${sourceCounts.get(s)})` }))]}
      onchange={(e) => setFilter('source', e.target.value)}
      ariaLabel="Filter by data source"
    />
  {/if}

  <!-- Clear all button -->
  {#if active}
    <button type="button" class="clear-btn" onclick={clearFilters}>
      Clear filters
    </button>
  {/if}
</div>

<style>
  .filter-bar {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    margin-bottom: var(--sp-4);
    flex-wrap: wrap;
  }

  .filter-search {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 4px 10px;
    border-radius: 100px;
    border: 1px solid var(--c-border);
    background: var(--c-surface);
    transition: border-color var(--transition);
    min-height: 30px;
    flex: 1;
    min-width: 180px;
  }

  .filter-search:focus-within {
    border-color: var(--c-accent);
  }

  .filter-search svg {
    color: var(--c-text-3);
    flex-shrink: 0;
  }

  .filter-search input {
    border: none;
    background: none;
    outline: none;
    font: inherit;
    font-size: var(--text-xs);
    color: var(--c-text);
    width: 100%;
    padding: 2px 0;
  }

  .filter-search input::placeholder {
    color: var(--c-text-3);
  }

  .clear-btn {
    font: inherit;
    font-size: var(--text-xs);
    padding: 4px 12px;
    border-radius: 100px;
    border: 1px solid var(--c-border);
    background: var(--c-surface);
    color: var(--c-text-2);
    cursor: pointer;
    transition: all var(--transition);
    min-height: 30px;
  }

  .clear-btn:hover {
    border-color: var(--c-accent);
    color: var(--c-accent);
  }
</style>
