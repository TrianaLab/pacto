<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { classificationClass } from '../lib/format.ts';
  import DiffChangesTable from '../DiffChangesTable.svelte';

  let {
    name = '',
    initialFrom = '', initialTo = '',
    initialFromName = '', initialToName = '',
    services = [],
  } = $props();

  // Service selection for from/to
  let fromName = $state(initialFromName || name || '');
  let toName = $state(initialToName || name || '');
  let fromVersions = $state([]);
  let toVersions = $state([]);
  let fromVer = $state(initialFrom);
  let toVer = $state(initialTo);
  let loading = $state(false);
  let error = $state(null);
  let result = $state(null);

  async function loadVersionsFor(svcName) {
    if (!svcName) return [];
    try {
      return await api.versions(svcName);
    } catch {
      return [];
    }
  }

  async function onFromNameChange() {
    fromVer = '';
    result = null;
    fromVersions = await loadVersionsFor(fromName);
  }

  async function onToNameChange() {
    toVer = '';
    result = null;
    toVersions = await loadVersionsFor(toName);
  }

  async function initVersions() {
    const [fv, tv] = await Promise.all([
      loadVersionsFor(fromName),
      loadVersionsFor(toName),
    ]);
    fromVersions = fv;
    toVersions = tv;

    // Auto-select versions if not provided via URL
    if (!fromVer && !toVer && fromName === toName && fromVersions?.length >= 2) {
      fromVer = fromVersions[1].version;
      toVer = fromVersions[0].version;
    }
    if (fromVer && toVer) runDiff();
  }

  async function runDiff() {
    if (!fromName || !toName || !fromVer || !toVer) return;
    loading = true;
    error = null;
    result = null;
    try {
      result = await api.diff(fromName, fromVer, toName, toVer);
    } catch (e) {
      error = e.message;
    }
    loading = false;
  }

  function swapSides() {
    const tmpName = fromName;
    const tmpVer = fromVer;
    const tmpVersions = fromVersions;
    fromName = toName;
    fromVer = toVer;
    fromVersions = toVersions;
    toName = tmpName;
    toVer = tmpVer;
    toVersions = tmpVersions;
    result = null;
  }

  // Service names for dropdowns
  let serviceNames = $derived(
    services.length > 0
      ? [...new Set(services.map((s) => s.name))].sort()
      : []
  );

  let isSameService = $derived(fromName === toName);

  onMount(() => { initVersions(); });
</script>

<nav class="breadcrumb" aria-label="Breadcrumb">
  <a href="#/">Services</a>
  <span class="sep">/</span>
  {#if name}
    <a href={serviceUrl(name)}>{name}</a>
    <span class="sep">/</span>
  {/if}
  <span>Diff</span>
</nav>

<h1 style="margin-bottom:var(--sp-5)">Compare Versions</h1>

<div class="diff-controls">
  <div class="diff-side">
    <div class="diff-field">
      <label for="from-svc">From service</label>
      {#if serviceNames.length > 0}
        <select id="from-svc" bind:value={fromName} onchange={onFromNameChange}>
          <option value="">Select service</option>
          {#each serviceNames as sn}
            <option value={sn}>{sn}</option>
          {/each}
        </select>
      {:else}
        <input id="from-svc" type="text" bind:value={fromName} onchange={onFromNameChange} placeholder="Service name" />
      {/if}
    </div>
    <div class="diff-field">
      <label for="from-ver">Version</label>
      <select id="from-ver" bind:value={fromVer}>
        <option value="">Select version</option>
        {#each fromVersions as v}
          <option value={v.version}>{v.version}</option>
        {/each}
      </select>
    </div>
  </div>

  <button type="button" class="btn-swap" onclick={swapSides} title="Swap sides" aria-label="Swap from and to">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><path d="M7 16l-4-4 4-4"/><path d="M17 8l4 4-4 4"/><line x1="3" y1="12" x2="21" y2="12"/></svg>
  </button>

  <div class="diff-side">
    <div class="diff-field">
      <label for="to-svc">To service</label>
      {#if serviceNames.length > 0}
        <select id="to-svc" bind:value={toName} onchange={onToNameChange}>
          <option value="">Select service</option>
          {#each serviceNames as sn}
            <option value={sn}>{sn}</option>
          {/each}
        </select>
      {:else}
        <input id="to-svc" type="text" bind:value={toName} onchange={onToNameChange} placeholder="Service name" />
      {/if}
    </div>
    <div class="diff-field">
      <label for="to-ver">Version</label>
      <select id="to-ver" bind:value={toVer}>
        <option value="">Select version</option>
        {#each toVersions as v}
          <option value={v.version}>{v.version}</option>
        {/each}
      </select>
    </div>
  </div>

  <button type="button" class="btn btn-primary diff-run" onclick={runDiff} disabled={!fromName || !toName || !fromVer || !toVer || loading}>
    {loading ? 'Comparing...' : 'Compare'}
  </button>
</div>

{#if error}
  <div class="insight insight-critical" style="margin-top:var(--sp-4)">{error}</div>
{/if}

{#if result}
  <div class="diff-result">
    <div class="diff-summary">
      <span class="badge {classificationClass(result.classification)}">{result.classification.replace(/_/g, ' ')}</span>
      <span class="text-2">{result.changes?.length || 0} change{(result.changes?.length ?? 0) !== 1 ? 's' : ''}</span>
      {#if !isSameService}
        <span class="text-3">({fromName} {fromVer} vs {toName} {toVer})</span>
      {/if}
    </div>

    <DiffChangesTable changes={result.changes || []} />
  </div>
{/if}

<style>
  .breadcrumb {
    font-size: var(--text-sm); margin-bottom: var(--sp-4);
    color: var(--c-text-3); display: flex; align-items: center; gap: 6px;
  }
  .breadcrumb a { color: var(--c-text-3); }
  .breadcrumb a:hover { color: var(--c-text); }
  .sep { color: var(--c-text-3); }

  .diff-controls {
    display: flex; align-items: flex-end; gap: var(--sp-3); flex-wrap: wrap;
    margin-bottom: var(--sp-5);
  }
  .diff-side {
    display: flex; gap: var(--sp-2); flex: 1; min-width: 200px;
  }
  .diff-field {
    display: flex; flex-direction: column; gap: 4px; flex: 1;
  }
  .diff-field label { font-size: var(--text-xs); color: var(--c-text-3); font-weight: 500; text-transform: uppercase; }
  .diff-field select, .diff-field input {
    padding: 6px 8px; border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm);
  }

  .btn-swap {
    display: flex; align-items: center; justify-content: center;
    padding: 6px; border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface); cursor: pointer; color: var(--c-text-2);
    transition: all var(--transition); align-self: flex-end;
  }
  .btn-swap:hover { border-color: var(--c-accent); color: var(--c-accent); }

  .diff-run { align-self: flex-end; white-space: nowrap; }

  .diff-result { margin-top: var(--sp-4); }
  .diff-summary { display: flex; align-items: center; gap: var(--sp-2); margin-bottom: var(--sp-4); }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-sm); }
</style>
