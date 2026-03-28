<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { serviceUrl } from '../lib/router.ts';

  let { config, open = $bindable(false), id = '' } = $props();
</script>

{#if config}
  <CollapsibleSection title="Configuration" bind:open {id}>
    {#if config.schema}<p class="text-2">Schema: <code>{config.schema}</code></p>{/if}
    {#if config.ref}
      <p class="text-2">Ref: <a href={serviceUrl(config.ref.split('/').pop().split(':')[0])}>{config.ref}</a></p>
    {/if}
    {#if config.values?.length > 0}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
          <tbody>
            {#each config.values as v}
              <tr>
                <td><code>{v.key}</code></td>
                <td>{v.value === '(any)' ? '—' : v.value}</td>
                <td><span class="pill">{v.type}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if config.valueKeys?.length > 0}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Key</th></tr></thead>
          <tbody>
            {#each config.valueKeys as key}
              <tr><td><code>{key}</code></td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if config.secretKeys?.length > 0}
      <div class="subsection">
        <h3>Secret Keys</h3>
        <div class="table-wrap">
          <table>
            <thead><tr><th>Key</th><th>Type</th></tr></thead>
            <tbody>
              {#each config.secretKeys as key}
                <tr><td><code>{key}</code></td><td><span class="pill" style="color:var(--c-warn)">secret</span></td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}
  </CollapsibleSection>
{/if}

<style>
  .subsection { margin-top: var(--sp-4); }
  .subsection h3 { margin-bottom: var(--sp-2); }
  .text-2 { color: var(--c-text-2); }
</style>
