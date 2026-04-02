<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import { methodClass } from '../lib/format.ts';

  let { interfaces = [], open = $bindable(true), id = '' } = $props();

  let expanded = $state({});

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }

  function hasDetails(iface) {
    return iface.endpoints?.length > 0 || iface.contractContent;
  }
</script>

{#if interfaces?.length > 0}
  <CollapsibleSection title="Interfaces" count={interfaces.length} bind:open {id}>
    {#each interfaces as iface, i}
      <div class="detail-card">
        <button type="button" class="detail-card-header" class:expandable={hasDetails(iface)} onclick={() => hasDetails(iface) && toggle(i)}>
          <div class="detail-card-header-left">
            {#if hasDetails(iface)}
              <span class="expand-icon" class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
            {/if}
            <span class="pill pill-type">{iface.type}</span>
            <span class="detail-card-title">{iface.name}</span>
            {#if iface.port != null}<span class="pill">:{iface.port}</span>{/if}
            {#if iface.visibility}<span class="pill">{iface.visibility}</span>{/if}
          </div>
          {#if iface.hasContractFile}
            <span class="detail-card-sub">{iface.contractFile || 'has contract'}</span>
          {/if}
        </button>

        {#if expanded[i] && hasDetails(iface)}
          <div class="detail-card-body">
            {#if iface.endpoints?.length > 0}
              <table class="detail-card-table">
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
            {/if}
            {#if iface.contractContent}
              <details class="contract-content">
                <summary>Raw contract</summary>
                <pre>{iface.contractContent}</pre>
              </details>
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
  .pill-type { background: var(--c-info-bg, var(--c-accent-bg)); color: var(--c-info, var(--c-accent)); font-size: var(--text-xs); flex-shrink: 0; }
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
  .detail-card-sub { font-size: var(--text-sm); color: var(--c-text-2); flex-shrink: 0; }
  .detail-card-body {
    padding: 0 var(--sp-3) var(--sp-3);
    animation: slideReveal 200ms ease-out both;
  }
  .detail-card-table { font-size: var(--text-sm); }
  .detail-card-table th { font-size: var(--text-xs); }
  .text-2 { color: var(--c-text-2); }
  .contract-content { margin-top: var(--sp-2); }
  .contract-content summary { cursor: pointer; color: var(--c-text-3); font-size: var(--text-sm); }

  @keyframes slideReveal {
    from { opacity: 0; transform: translateY(-4px); }
    to { opacity: 1; transform: translateY(0); }
  }
</style>
