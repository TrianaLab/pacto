<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { serviceUrl } from '../lib/router.ts';

  let { policies = [], open = $bindable(false), id = '' } = $props();

  let hasContent = $derived(policies?.length > 0);
  let expanded = $state({});

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }

  function refServiceName(ref) {
    return ref.split('/').pop().split(':')[0];
  }
</script>

{#if hasContent}
  <CollapsibleSection title="Policies" count={policies.length} bind:open {id}>
    {#each policies as pol, i}
      <div class="detail-card">
        <button type="button" class="detail-card-header" class:expandable={pol.values?.length > 0} onclick={() => pol.values?.length > 0 && toggle(i)}>
          <div class="detail-card-header-left">
            {#if pol.values?.length > 0}
              <span class="expand-icon" class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
            {/if}
            <span class="pill {pol.ref ? 'pill-ref' : 'pill-local'}">{pol.ref ? 'Remote' : 'Local'}</span>
            <span class="detail-card-title">{pol.name}</span>
            {#if pol.title && pol.title !== pol.name}
              <span class="detail-card-sub">{pol.title}</span>
            {:else if pol.schema}
              <code class="detail-card-sub">{pol.schema}</code>
            {/if}
          </div>
          {#if pol.ref}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <a href={serviceUrl(refServiceName(pol.ref))} class="ref-link" onclick={(e) => e.stopPropagation()}>
              {pol.ref} →
            </a>
          {/if}
        </button>

        {#if pol.description}
          <p class="detail-card-desc">{pol.description}</p>
        {/if}

        {#if expanded[i] && pol.values?.length > 0}
          <div class="detail-card-body">
            <table class="detail-card-table">
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
  .detail-card-desc {
    font-size: var(--text-sm);
    color: var(--c-text-2);
    line-height: 1.5;
    margin: 0;
    padding: 0 var(--sp-3) var(--sp-3);
  }
  .detail-card-body {
    padding: 0 var(--sp-3) var(--sp-3);
    animation: slideReveal 200ms ease-out both;
  }
  .detail-card-table { font-size: var(--text-sm); }
  .detail-card-table th { font-size: var(--text-xs); }

  @keyframes slideReveal {
    from { opacity: 0; transform: translateY(-4px); }
    to { opacity: 1; transform: translateY(0); }
  }
</style>
