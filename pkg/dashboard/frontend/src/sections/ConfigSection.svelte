<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { serviceUrl } from '../lib/router.ts';

  let { configs = [], open = $bindable(false), id = '' } = $props();

  let hasContent = $derived(configs?.length > 0);
  let expanded = $state({});

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }

  function hasDetails(config) {
    return config.values?.length > 0 || config.valueKeys?.length > 0 || config.secretKeys?.length > 0;
  }

  function refServiceName(ref) {
    return ref.split('/').pop().split(':')[0];
  }
</script>

{#if hasContent}
  <CollapsibleSection title="Configuration" count={configs.length > 1 ? configs.length : null} bind:open {id}>
    {#each configs as config, i}
      <div class="detail-card">
        <button type="button" class="detail-card-header" class:expandable={hasDetails(config)} onclick={() => hasDetails(config) && toggle(i)}>
          <div class="detail-card-header-left">
            {#if hasDetails(config)}
              <span class="expand-icon" class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
            {/if}
            <span class="pill {config.ref ? 'pill-ref' : 'pill-local'}">{config.ref ? 'Remote' : 'Local'}</span>
            {#if config.name}
              <span class="detail-card-title">{config.name}</span>
            {/if}
            {#if config.schema}
              <code class="detail-card-sub">{config.schema}</code>
            {/if}
          </div>
          {#if config.ref}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <a href={serviceUrl(refServiceName(config.ref))} class="ref-link" onclick={(e) => e.stopPropagation()}>
              {config.ref} →
            </a>
          {/if}
        </button>

        {#if expanded[i] && hasDetails(config)}
          <div class="detail-card-body">
            {#if config.values?.length > 0}
              <table class="detail-card-table">
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
            {:else if config.valueKeys?.length > 0}
              <table class="detail-card-table">
                <thead><tr><th>Key</th></tr></thead>
                <tbody>
                  {#each config.valueKeys as key}
                    <tr><td><code>{key}</code></td></tr>
                  {/each}
                </tbody>
              </table>
            {/if}
            {#if config.secretKeys?.length > 0}
              <div class="detail-card-sub-section">
                <h4>Secret Keys</h4>
                <table class="detail-card-table">
                  <thead><tr><th>Key</th><th>Type</th></tr></thead>
                  <tbody>
                    {#each config.secretKeys as key}
                      <tr><td><code>{key}</code></td><td><span class="pill" style="color:var(--c-warn)">secret</span></td></tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          </div>
        {/if}
      </div>
    {/each}
  </CollapsibleSection>
{/if}

<style>
  .detail-card {
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    background: var(--c-surface);
    margin-bottom: var(--sp-2);
  }
  .detail-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: var(--sp-3);
    background: none;
    border: none;
    font: inherit;
    color: var(--c-text);
    text-align: left;
    gap: var(--sp-3);
  }
  .detail-card-header.expandable { cursor: pointer; }
  .detail-card-header.expandable:hover { background: var(--c-surface-hover, var(--c-surface-inset)); border-radius: var(--radius-sm); }
  .detail-card-header-left {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    min-width: 0;
  }
  .pill-ref { background: var(--c-accent-bg); color: var(--c-accent); font-size: var(--text-xs); flex-shrink: 0; }
  .pill-local { background: var(--c-neutral-bg); color: var(--c-text-2); font-size: var(--text-xs); flex-shrink: 0; }
  .expand-icon {
    display: inline-flex;
    color: var(--c-text-3);
    transition: transform 200ms ease;
    transform: rotate(-90deg);
    flex-shrink: 0;
  }
  .expand-icon.open { transform: rotate(0deg); }
  .expand-icon svg { width: 12px; height: 12px; }
  .detail-card-title { font-weight: 600; }
  .detail-card-sub { font-size: var(--text-sm); color: var(--c-text-2); }
  .ref-link {
    font-size: var(--text-xs);
    color: var(--c-accent);
    text-decoration: none;
    white-space: nowrap;
    flex-shrink: 0;
  }
  .ref-link:hover { text-decoration: underline; }
  .detail-card-body {
    padding: 0 var(--sp-3) var(--sp-3);
    animation: slideReveal 200ms ease-out both;
  }
  .detail-card-table { font-size: var(--text-sm); }
  .detail-card-table th { font-size: var(--text-xs); }
  .detail-card-sub-section { margin-top: var(--sp-3); }
  .detail-card-sub-section h4 { margin-bottom: var(--sp-2); font-size: var(--text-sm); font-weight: 600; }

  @keyframes slideReveal {
    from { opacity: 0; transform: translateY(-4px); }
    to { opacity: 1; transform: translateY(0); }
  }
</style>
