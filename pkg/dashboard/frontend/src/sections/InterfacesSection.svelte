<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { methodClass } from '../lib/format.ts';

  let { interfaces = [], open = $bindable(true), id = '' } = $props();
</script>

{#if interfaces?.length > 0}
  <CollapsibleSection title="Interfaces" count={interfaces.length} bind:open {id}>
    {#each interfaces as iface}
      <div class="card iface-card">
        <div class="iface-header">
          <strong>{iface.name}</strong>
          <span class="badge badge-info">{iface.type}</span>
          {#if iface.port != null}<span class="pill">:{iface.port}</span>{/if}
          {#if iface.visibility}<span class="pill">{iface.visibility}</span>{/if}
          {#if iface.hasContractFile}<span class="pill" title={iface.contractFile}>has contract</span>{/if}
        </div>
        {#if iface.endpoints?.length > 0}
          <div class="table-wrap">
            <table>
              <thead><tr><th>Method</th><th>Path</th><th>Summary</th></tr></thead>
              <tbody>
                {#each iface.endpoints as ep}
                  <tr>
                    <td><span class="badge {methodClass(ep.method)}">{ep.method}</span></td>
                    <td><code>{ep.path}</code></td>
                    <td class="text-2">{ep.summary || ''}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
        {#if iface.contractContent}
          <details class="contract-content">
            <summary>Contract content</summary>
            <pre>{iface.contractContent}</pre>
          </details>
        {/if}
      </div>
    {/each}
  </CollapsibleSection>
{/if}

<style>
  .iface-card { margin-bottom: var(--sp-3); }
  .iface-header { display: flex; align-items: center; gap: var(--sp-2); margin-bottom: var(--sp-2); flex-wrap: wrap; }
  .contract-content { margin-top: var(--sp-2); }
  .contract-content summary { cursor: pointer; color: var(--c-text-3); font-size: var(--text-sm); }
  .text-2 { color: var(--c-text-2); }
</style>
