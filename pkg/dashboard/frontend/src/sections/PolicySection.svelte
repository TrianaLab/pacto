<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { serviceUrl } from '../lib/router.ts';

  let { policy, open = $bindable(false), id = '' } = $props();
</script>

{#if policy}
  <CollapsibleSection title="Policy" bind:open {id}>
    {#if policy.schema}<p class="text-2">Schema: <code>{policy.schema}</code></p>{/if}
    {#if policy.ref}
      <p class="text-2">Ref: <a href={serviceUrl(policy.ref.split('/').pop().split(':')[0])}>{policy.ref}</a></p>
    {/if}
    {#if policy.values?.length > 0}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
          <tbody>
            {#each policy.values as v}
              <tr>
                <td><code>{v.key}</code></td>
                <td>{v.value === '(any)' ? '—' : v.value}</td>
                <td><span class="pill">{v.type}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if policy.content}
      <details><summary>Raw content</summary><pre>{policy.content}</pre></details>
    {/if}
  </CollapsibleSection>
{/if}

<style>
  .text-2 { color: var(--c-text-2); }
</style>
