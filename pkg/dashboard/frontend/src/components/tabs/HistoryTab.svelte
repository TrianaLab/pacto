<script>
  import { api } from '../../lib/api.js';
  import { serviceDetails, serviceVersions, currentTab } from '../../lib/stores.js';
  import { classificationBadgeClass, classificationLabel } from '../../lib/helpers.js';

  let { versions, serviceName } = $props();

  let fetching = $state(false);
  let fetchError = $state(null);

  let currentVersion = $derived($serviceDetails[serviceName]?.version);

  // Detect OCI repo for "Fetch all versions"
  let ociRepo = $derived.by(() => {
    for (const v of versions) {
      if (v.ref) {
        const idx = v.ref.lastIndexOf(':');
        if (idx > 0) return v.ref.substring(0, idx);
      }
    }
    return '';
  });

  async function fetchAllVersions() {
    if (!ociRepo) return;
    fetching = true;
    fetchError = null;
    try {
      await api.listRemoteVersions(ociRepo, true);
      const newVersions = await api.getVersions(serviceName);
      serviceVersions.update((cur) => ({ ...cur, [serviceName]: newVersions }));
    } catch (e) {
      fetchError = e.message;
    }
    fetching = false;
  }

  function compareVersion(version) {
    currentTab.set('diff');
    // The DiffTab will handle the pre-selection via its own logic
  }
</script>

{#if !versions.length}
  <div class="card">
    <div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No revision history available</div>
  </div>
{:else}
  <div class="card">
    <div class="card-header">
      <div class="section-label">Version History</div>
      <span class="text-dim">{versions.length} revision{versions.length !== 1 ? 's' : ''}</span>
      {#if ociRepo}
        <button class="filter-clear" style="font-size:11px;margin-left:8px" onclick={fetchAllVersions} disabled={fetching}>
          {fetching ? 'Fetching\u2026' : 'Fetch all versions'}
        </button>
      {/if}
    </div>
    {#if fetchError}
      <div style="color:var(--critical);font-size:var(--text-sm);margin-bottom:8px">{fetchError}</div>
    {/if}
    <div class="table-wrapper">
      <table>
        <thead><tr><th>Version</th><th>Source</th><th>Hash</th><th>Created</th><th>Changes</th><th>Status</th><th></th></tr></thead>
        <tbody>
          {#each versions as v}
            {@const isCurrent = v.version === currentVersion}
            <tr>
              <td><strong>{v.version}</strong></td>
              <td><span class="text-dim">{v.ref || '\u2014'}</span></td>
              <td><code>{v.contractHash ? v.contractHash.substring(0, 12) : '\u2014'}</code></td>
              <td><span class="text-dim">{v.createdAt ? new Date(v.createdAt).toLocaleDateString() : '\u2014'}</span></td>
              <td>
                {#if v.classification}
                  <span class="badge {classificationBadgeClass(v.classification)}">{classificationLabel(v.classification)}</span>
                {:else}
                  <span class="text-dim">&mdash;</span>
                {/if}
              </td>
              <td>
                {#if isCurrent}
                  <span class="badge badge-ok">current</span>
                {:else}
                  <span class="badge badge-neutral">{v.version}</span>
                {/if}
              </td>
              <td>
                {#if versions.length > 1 && !isCurrent}
                  <button class="filter-clear" style="font-size:11px" onclick={() => compareVersion(v.version)}>Compare with current</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}
