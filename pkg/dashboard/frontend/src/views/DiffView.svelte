<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { serviceUrl } from '../lib/router.js';

  let { name } = $props();

  let versions = $state([]);
  let fromVer = $state('');
  let toVer = $state('');
  let loading = $state(false);
  let error = $state(null);
  let result = $state(null);

  async function loadVersions() {
    try {
      versions = await api.versions(name);
      if (versions.length >= 2) {
        fromVer = versions[1].version;
        toVer = versions[0].version;
      } else if (versions.length === 1) {
        toVer = versions[0].version;
      }
    } catch {}
  }

  async function runDiff() {
    if (!fromVer || !toVer) return;
    loading = true;
    error = null;
    result = null;
    try {
      result = await api.diff(name, fromVer, name, toVer);
    } catch (e) {
      error = e.message;
    }
    loading = false;
  }

  function classificationClass(c) {
    if (c === 'BREAKING') return 'badge-err';
    if (c === 'POTENTIAL_BREAKING') return 'badge-warn';
    if (c === 'NON_BREAKING') return 'badge-ok';
    return 'badge-neutral';
  }

  function changeTypeClass(t) {
    if (t === 'added') return 'diff-added';
    if (t === 'removed') return 'diff-removed';
    if (t === 'modified') return 'diff-modified';
    return '';
  }

  function formatValue(val) {
    if (val == null) return '—';
    if (typeof val === 'object') return JSON.stringify(val, null, 2);
    return String(val);
  }

  onMount(() => { loadVersions(); });
</script>

<nav class="breadcrumb" aria-label="Breadcrumb">
  <a href="#/">Services</a>
  <span class="sep">/</span>
  <a href={serviceUrl(name)}>{name}</a>
  <span class="sep">/</span>
  <span>Diff</span>
</nav>

<h1 style="margin-bottom:var(--sp-5)">Compare Versions</h1>

<div class="diff-controls">
  <div class="diff-field">
    <label for="from-ver">From</label>
    <select id="from-ver" bind:value={fromVer}>
      <option value="">Select version</option>
      {#each versions as v}
        <option value={v.version}>{v.version}</option>
      {/each}
    </select>
  </div>
  <div class="diff-field">
    <label for="to-ver">To</label>
    <select id="to-ver" bind:value={toVer}>
      <option value="">Select version</option>
      {#each versions as v}
        <option value={v.version}>{v.version}</option>
      {/each}
    </select>
  </div>
  <button type="button" class="btn btn-primary" onclick={runDiff} disabled={!fromVer || !toVer || loading}>
    {loading ? 'Comparing…' : 'Compare'}
  </button>
</div>

{#if error}
  <div class="insight insight-critical" style="margin-top:var(--sp-4)">{error}</div>
{/if}

{#if result}
  <div class="diff-result">
    <div class="diff-summary">
      <span class="badge {classificationClass(result.classification)}">{result.classification.replace(/_/g, ' ')}</span>
      <span class="text-2">{result.changes.length} change{result.changes.length !== 1 ? 's' : ''}</span>
    </div>

    {#if result.changes.length === 0}
      <div class="state-box"><h3>No changes detected</h3></div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Path</th><th>Change</th><th>Old value</th><th>New value</th><th>Impact</th></tr></thead>
          <tbody>
            {#each result.changes as change}
              <tr>
                <td><code>{change.path}</code></td>
                <td><span class={changeTypeClass(change.type)}>{change.type}</span></td>
                <td><pre class="diff-value">{formatValue(change.oldValue)}</pre></td>
                <td><pre class="diff-value">{formatValue(change.newValue)}</pre></td>
                <td>
                  <span class="badge {classificationClass(change.classification)}">{change.classification.replace(/_/g, ' ')}</span>
                  {#if change.reason}<br><span class="text-3" style="font-size:var(--text-xs)">{change.reason}</span>{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
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
  .diff-field {
    display: flex; flex-direction: column; gap: 4px;
  }
  .diff-field label { font-size: var(--text-xs); color: var(--c-text-3); font-weight: 500; text-transform: uppercase; }

  .diff-result { margin-top: var(--sp-4); }
  .diff-summary { display: flex; align-items: center; gap: var(--sp-2); margin-bottom: var(--sp-4); }

  .diff-value {
    font-size: var(--text-xs);
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    margin: 0;
    padding: 2px 4px;
    background: var(--c-surface-inset);
    border-radius: var(--radius-xs);
  }

  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
</style>
