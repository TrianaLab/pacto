<script>
  import { api } from '../../lib/api.js';

  let { versions, serviceName } = $props();

  let fromVersion = $state(versions[1]?.version || '');
  let toVersion = $state(versions[0]?.version || '');
  let loading = $state(false);
  let error = $state(null);
  let result = $state(null);

  async function runDiff() {
    if (!fromVersion || !toVersion) return;
    loading = true;
    error = null;
    result = null;
    try {
      result = await api.getDiff(
        { name: serviceName, version: fromVersion },
        { name: serviceName, version: toVersion }
      );
    } catch (e) {
      error = e.message;
    }
    loading = false;
  }

  function changeBadgeClass(classification) {
    if (classification === 'BREAKING') return 'badge-critical';
    if (classification === 'NON_BREAKING') return 'badge-ok';
    return 'badge-warning';
  }
</script>

{#if versions.length < 2}
  <div class="card">
    <div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">At least two revisions are needed to compare versions.</div>
  </div>
{:else}
  <div class="card">
    <div class="card-header">
      <div class="section-label">Compare Revisions</div>
      <span class="text-dim">{versions.length} revisions available</span>
    </div>
    <div class="selector-form" style="margin-bottom:0">
      <div>
        <label for="diff-from">From</label>
        <select id="diff-from" bind:value={fromVersion}>
          {#each versions as v}<option value={v.version}>{v.version}</option>{/each}
        </select>
      </div>
      <div>
        <label for="diff-to">To</label>
        <select id="diff-to" bind:value={toVersion}>
          {#each versions as v}<option value={v.version}>{v.version}</option>{/each}
        </select>
      </div>
      <div>
        <label>&nbsp;</label>
        <button type="button" onclick={runDiff}>Compare</button>
      </div>
    </div>
  </div>

  <div style="margin-top:16px">
    {#if loading}
      <div class="loading"><div class="spinner"></div>Comparing...</div>
    {:else if error}
      <div style="background:var(--critical-bg);color:var(--critical);padding:14px 18px;border-radius:var(--radius-sm);border:1px solid var(--critical-border);font-size:var(--text-sm)">{error}</div>
    {:else if result}
      {#if result.classification}
        <div class="classification-banner classification-{result.classification}">{result.classification.replace(/_/g, ' ')}</div>
      {/if}
      {#if result.changes?.length}
        <div class="card">
          <div class="table-wrapper">
            <table class="diff-table">
              <colgroup><col style="width:25%"><col style="width:12%"><col style="width:20%"><col style="width:20%"><col style="width:23%"></colgroup>
              <thead><tr><th>Path</th><th>Type</th><th>Old</th><th>New</th><th class="hide-narrow">Reason</th></tr></thead>
              <tbody>
                {#each result.changes as c}
                  <tr>
                    <td><code>{c.path}</code></td>
                    <td><span class="badge {changeBadgeClass(c.classification)}">{c.type}</span></td>
                    <td>{#if c.oldValue != null}<span class="diff-old">{String(c.oldValue)}</span>{:else}<span class="text-dim">&mdash;</span>{/if}</td>
                    <td>{#if c.newValue != null}<span class="diff-new">{String(c.newValue)}</span>{:else}<span class="text-dim">&mdash;</span>{/if}</td>
                    <td class="hide-narrow"><span class="diff-reason">{c.reason || ''}</span></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </div>
      {:else}
        <div style="color:var(--text-dim);font-size:var(--text-sm);padding:16px">No changes detected between these versions.</div>
      {/if}
    {/if}
  </div>
{/if}
