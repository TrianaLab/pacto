<script>
  import ShowAll from '../components/ShowAll.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SectionState from './SectionState.svelte';
  import { methodClass } from '../lib/format.ts';

  let { interfaces = [], open = $bindable(true), id = '' } = $props();

  let expanded = $state({});

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }

  function hasDetails(iface) {
    return iface.endpoints?.length > 0 || iface.contractContent;
  }
  // Eight hundred endpoints show the first fifty; one cap for every interface on the page.
  const eps = cap(50);
</script>

{#if interfaces?.length > 0}
  <CollapsibleSection title="Interfaces" count={interfaces.length} bind:open {id}>
    {#each interfaces as iface, i}
      <div class="detail-card">
        <button type="button" class="detail-card-header" class:expandable={hasDetails(iface)} onclick={() => hasDetails(iface) && toggle(i)}>
          <div class="detail-card-header-left">
            {#if hasDetails(iface)}
              <span class="expand-icon" data-motion class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
            {/if}
            <span class="pill pill-type">{iface.type}</span>
            <span class="detail-card-title">{iface.name}</span>
            {#if iface.port != null}<span class="pill">:{iface.port}</span>{/if}
            {#if iface.visibility}<span class="pill">{iface.visibility}</span>{/if}
          </div>
          {#if iface.hasContractFile}
            <span class="detail-card-sub" title={iface.contractFile || undefined}>{iface.contractFile || 'has contract'}</span>
          {/if}
        </button>

        {#if expanded[i] && hasDetails(iface)}
          <div class="detail-card-body">
            {#if iface.endpoints?.length > 0}
              <table class="detail-card-table">
                <thead><tr><th>Method</th><th>Path</th><th>Summary</th></tr></thead>
                <tbody>
                  {#each eps.slice(iface.endpoints) as ep, ei (ei)}
                    <tr>
                      <td><span class="badge {methodClass(ep.method)}">{ep.method}</span></td>
                      <td><code>{ep.path}</code></td>
                      <td class="text-2">{ep.summary || ''}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
              <ShowAll cap={eps} total={iface.endpoints.length} noun="endpoints" />
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
{:else}
  <SectionState title="Interfaces" bind:open {id} />
{/if}

<style>
  .detail-card-header-left { flex-wrap: wrap; flex: 1 1 16rem; }
  .pill-type { background: var(--c-info-bg, var(--c-accent-bg)); color: var(--c-info, var(--c-accent)); font-size: var(--text-xs); flex-shrink: 0; }
  /* The contract file ellipsizes rather than pushing the interface name to one word per line. */
  .detail-card-sub { max-width: 40%; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .text-2 { color: var(--c-text-2); }
  .contract-content { margin-top: var(--sp-2); }
  .contract-content summary { cursor: pointer; color: var(--c-text-3); font-size: var(--text-sm); }
</style>
