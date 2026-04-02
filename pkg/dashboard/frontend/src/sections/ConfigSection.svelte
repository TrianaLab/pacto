<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { serviceUrl } from '../lib/router.ts';

  let { configs = [], open = $bindable(false), id = '' } = $props();

  let hasContent = $derived(configs?.length > 0);
  let isMulti = $derived(configs?.length > 1 || (configs?.length === 1 && configs[0].name));
</script>

{#if hasContent}
  <CollapsibleSection title="Configuration" count={isMulti ? configs.length : null} bind:open {id}>
    {#each configs as config, i}
      {#if isMulti}
        <div class="config-scope" class:config-scope-border={i > 0}>
          <h3 class="scope-name">{config.name || 'default'}</h3>
          {@render configBody(config)}
        </div>
      {:else}
        {@render configBody(config)}
      {/if}
    {/each}
  </CollapsibleSection>
{/if}

{#snippet configBody(config)}
  <div class="config-meta">
    {#if config.ref}
      <span class="pill pill-ref">ref</span>
      <a href={serviceUrl(config.ref.split('/').pop().split(':')[0])} class="ref-link">{config.ref}</a>
    {:else if config.schema}
      <span class="pill pill-local">local</span>
      <code class="text-3">{config.schema}</code>
    {/if}
  </div>
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
      <h4>Secret Keys</h4>
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
{/snippet}

<style>
  .config-scope { margin-bottom: var(--sp-4); }
  .config-scope-border { padding-top: var(--sp-4); border-top: 1px solid var(--c-border); }
  .scope-name {
    font-size: var(--text-sm); font-weight: 600;
    margin-bottom: var(--sp-2); color: var(--c-text);
  }
  .config-meta {
    display: flex; align-items: center; gap: var(--sp-2);
    margin-bottom: var(--sp-3);
  }
  .pill-ref { background: var(--c-accent-bg); color: var(--c-accent); font-size: var(--text-xs); }
  .pill-local { background: var(--c-neutral-bg); color: var(--c-text-2); font-size: var(--text-xs); }
  .ref-link { font-size: var(--text-sm); word-break: break-all; }
  .subsection { margin-top: var(--sp-4); }
  .subsection h4 { margin-bottom: var(--sp-2); font-size: var(--text-sm); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-sm); }
</style>
