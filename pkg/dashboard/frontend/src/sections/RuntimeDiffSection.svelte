<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';

  let { runtimeDiff = [], open = $bindable(false), id = '' } = $props();
</script>

{#if runtimeDiff?.length > 0}
  <CollapsibleSection title="Contract vs Runtime" bind:open {id}>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Field</th><th>Declared</th><th>Observed</th><th>Status</th></tr></thead>
        <tbody>
          {#each runtimeDiff as row}
            <tr>
              <td><strong>{row.field}</strong><br><code class="text-3">{row.contractPath}</code></td>
              <td>{row.declaredValue || '—'}</td>
              <td>{row.observedValue || '—'}</td>
              <td>
                {#if row.status === 'match'}<span class="badge badge-ok">Match</span>
                {:else if row.status === 'mismatch'}<span class="badge badge-err">Mismatch</span>
                {:else}<span class="badge badge-neutral">{row.status}</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </CollapsibleSection>
{/if}

<style>
  .text-3 { color: var(--c-text-3); }
</style>
