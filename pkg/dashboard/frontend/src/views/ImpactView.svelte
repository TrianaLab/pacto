<script>
  import { api, ApiError } from '../lib/api.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { classificationClass, changeTypeClass, completenessClass, completenessLabel } from '../lib/format.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import EmptyState from '../components/EmptyState.svelte';

  let oldRef = $state('');
  let newRef = $state('');
  let includeObserved = $state(false);
  let loading = $state(false);
  let error = $state(null);
  let result = $state(null);

  // Verdict coloring: an incompatible consumer is a confirmed break (red), a
  // compatible one is safe (green), and unknown is genuine uncertainty (neutral).
  function verdictClass(v) {
    if (v === 'compatible') return 'badge-ok';
    if (v === 'incompatible') return 'badge-err';
    return 'badge-neutral';
  }

  async function analyze(e) {
    e?.preventDefault();
    if (!oldRef || !newRef || loading) return;
    loading = true;
    error = null;
    result = null;
    try {
      result = await api.fleetImpact(oldRef, newRef, includeObserved);
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Couldn’t analyze the impact.';
    }
    loading = false;
  }

  const consumers = $derived(result?.consumers || []);
  const breakingChanges = $derived(result?.breakingChanges || []);
  const owners = $derived(result?.owners || []);
  const activeTargets = $derived(result?.activeTargets || []);
</script>

<div class="page-header">
  <h1>Impact</h1>
</div>

<form class="impact-form" onsubmit={analyze}>
  <div class="field">
    <label for="impact-old">Old ref</label>
    <input id="impact-old" type="text" bind:value={oldRef} placeholder="oci://ghcr.io/org/svc:1.0.0" />
  </div>
  <div class="field">
    <label for="impact-new">New ref</label>
    <input id="impact-new" type="text" bind:value={newRef} placeholder="oci://ghcr.io/org/svc:2.0.0" />
  </div>
  <label class="check-field">
    <input type="checkbox" bind:checked={includeObserved} />
    Include observed
  </label>
  <button type="submit" class="btn btn-primary" disabled={!oldRef || !newRef || loading}>
    {loading ? 'Analyzing...' : 'Analyze'}
  </button>
</form>

{#if error}
  <EmptyState error={error} onRetry={analyze} title="Couldn’t analyze the impact" />
{:else if loading}
  <EmptyState loading message="Analyzing the change…" />
{:else if result}
  <div class="impact-summary">
    <span class="badge {classificationClass(result.classification)}">{result.classification.replace(/_/g, ' ')}</span>
    <span class="text-2">{result.service}{#if result.oldVersion || result.newVersion} · {result.oldVersion || '?'} → {result.newVersion || '?'}{/if}</span>
    <span class="badge {completenessClass(result.completeness)}"><span class="badge-dot"></span>{completenessLabel(result.completeness)}</span>
    {#if result.asOf}<span class="text-3">as of {formatDate(result.asOf)}</span>{/if}
  </div>

  {#if breakingChanges.length > 0}
    <div class="section">
      <div class="section-title">Breaking changes <span class="tab-count">{breakingChanges.length}</span></div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Path</th><th>Type</th><th>Classification</th><th>Reason</th></tr></thead>
          <tbody>
            {#each breakingChanges as c}
              <tr>
                <td><code>{c.path}</code></td>
                <td><span class="badge {changeTypeClass(c.type)}">{c.type}</span></td>
                <td><span class="badge {classificationClass(c.classification)}">{c.classification.replace(/_/g, ' ')}</span></td>
                <td class="text-2">{c.reason}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}

  <div class="section" style="margin-top:var(--sp-6)">
    <div class="section-title">Affected consumers <span class="tab-count">{consumers.length}</span></div>
    {#if consumers.length === 0}
      <EmptyState title="No affected consumers" message="No service in the operational graph consumes this change." />
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Service</th>
              <th data-tip="Direct consumers depend on the changed service; transitive ones are reached through others">Reach</th>
              <th>Compatibility</th>
              <th data-tip="How strongly the impact on this consumer is evidenced">Confidence</th>
              <th>Provenance</th>
              <th>Owner</th>
              <th data-tip="Operational targets running this consumer">Targets</th>
            </tr>
          </thead>
          <tbody>
            {#each consumers as c}
              <tr>
                <td><a href={serviceUrl(c.service)}>{c.service}</a></td>
                <td>
                  {#if c.direct}<span class="badge badge-info">Direct</span>{:else}<span class="badge badge-neutral">Transitive (depth {c.depth})</span>{/if}
                </td>
                <td><span class="badge {verdictClass(c.compatibilityVerdict)}">{c.compatibilityVerdict}</span></td>
                <td><span class="pill">{c.confidence}</span></td>
                <td class="text-2">{c.provenance}</td>
                <td>{#if c.owner}{c.owner}{:else}<span class="text-dim">—</span>{/if}</td>
                <td>{(c.targets || []).length}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>

  <div class="meta-lists">
    <div class="meta-block">
      <div class="section-title">Owners <span class="tab-count">{owners.length}</span></div>
      {#if owners.length > 0}
        <div class="chips">{#each owners as o}<span class="pill">{o}</span>{/each}</div>
      {:else}
        <p class="text-dim">No owners identified.</p>
      {/if}
    </div>
    <div class="meta-block">
      <div class="section-title">Active targets <span class="tab-count">{activeTargets.length}</span></div>
      {#if activeTargets.length > 0}
        <div class="chips">{#each activeTargets as t}<span class="pill">{t}</span>{/each}</div>
      {:else}
        <p class="text-dim">No active targets.</p>
      {/if}
    </div>
  </div>
{/if}

<style>
  .page-header {
    display: flex; align-items: center; gap: var(--sp-3);
    margin-bottom: var(--sp-5); flex-wrap: wrap;
  }

  .impact-form {
    display: flex; align-items: flex-end; gap: var(--sp-3); flex-wrap: wrap;
    margin-bottom: var(--sp-5);
  }
  .field { display: flex; flex-direction: column; gap: 6px; flex: 1; min-width: 220px; }
  .field label { font-size: var(--text-xs); color: var(--c-text-3); font-weight: 500; text-transform: uppercase; }
  .field input {
    padding: var(--sp-2) var(--sp-3); min-height: var(--touch-min);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-bg); color: var(--c-text); font: inherit; font-size: var(--text-sm);
  }
  .check-field {
    display: inline-flex; align-items: center; gap: var(--sp-2);
    font-size: var(--text-sm); color: var(--c-text-2); white-space: nowrap;
    min-height: var(--touch-min);
  }

  .impact-summary {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    margin-bottom: var(--sp-5);
  }

  .meta-lists {
    display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: var(--sp-4); margin-top: var(--sp-6);
  }
  .chips { display: flex; flex-wrap: wrap; gap: var(--sp-2); margin-top: var(--sp-2); }

  .text-dim { color: var(--c-text-3); }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-sm); }

  @media (max-width: 768px) {
    .impact-form { flex-direction: column; align-items: stretch; }
    .field { min-width: 0; }
  }
</style>
