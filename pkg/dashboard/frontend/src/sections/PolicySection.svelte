<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { serviceUrl } from '../lib/router.ts';

  let { policies = [], open = $bindable(false), id = '' } = $props();

  let hasContent = $derived(policies?.length > 0);
</script>

{#if hasContent}
  <CollapsibleSection title="Policies" count={policies.length} bind:open {id}>
    <div class="table-wrap">
      <table>
        <thead><tr><th>#</th><th>Type</th><th>Source</th></tr></thead>
        <tbody>
          {#each policies as pol, i}
            <tr>
              <td>{i + 1}</td>
              <td><span class="pill {pol.ref ? 'pill-ref' : 'pill-local'}">{pol.ref ? 'Remote' : 'Local'}</span></td>
              <td>
                {#if pol.ref}
                  <a href={serviceUrl(pol.ref.split('/').pop().split(':')[0])}>{pol.ref}</a>
                {:else if pol.schema}
                  <code>{pol.schema}</code>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    {#each policies as pol, i}
      {#if pol.values?.length > 0 || pol.content}
        <details class="policy-detail">
          <summary>Policy {i + 1} details</summary>
          {#if pol.values?.length > 0}
            <div class="table-wrap">
              <table>
                <thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead>
                <tbody>
                  {#each pol.values as v}
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
          {#if pol.content}
            <pre class="policy-raw">{pol.content}</pre>
          {/if}
        </details>
      {/if}
    {/each}
  </CollapsibleSection>
{/if}

<style>
  .pill-ref { background: var(--c-accent-bg); color: var(--c-accent); font-size: var(--text-xs); }
  .pill-local { background: var(--c-neutral-bg); color: var(--c-text-2); font-size: var(--text-xs); }
  .policy-detail {
    margin-top: var(--sp-3);
    font-size: var(--text-sm);
  }
  .policy-detail summary {
    cursor: pointer;
    color: var(--c-text-2);
    font-weight: 500;
    padding: var(--sp-2) 0;
  }
  .policy-raw {
    margin-top: var(--sp-2);
    font-size: var(--text-xs);
    max-height: 300px;
    overflow: auto;
    background: var(--c-surface-inset);
    padding: var(--sp-3);
    border-radius: var(--radius-sm);
  }
</style>
